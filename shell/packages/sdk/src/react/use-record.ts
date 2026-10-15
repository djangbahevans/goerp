import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { createResource, deleteResource, getResource, updateResource } from "../api/resource-api.js";
import { authMachine, sessionIdentity } from "../auth/auth-machine.js";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { FieldOf, RecordOf, ResourceName, UpdateOf } from "../names.js";
import { type PresenceUser, useRecordPresence } from "../realtime/use-presence.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

import { recordQueryKey } from "./record-query-key.js";
import { useRecordAvailability } from "./use-record-availability.js";

export { recordQueryKey } from "./record-query-key.js";

export function createRecordQueryOptions<T>(
  resource: string,
  id: string | undefined,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordQueryKey(resource, id),
    queryFn: ({ signal }: { signal?: AbortSignal } = {}): Promise<T> =>
      getResource<T>(resource, id as string, registry, client, signal === undefined ? undefined : { signal }),
    enabled: id !== undefined,
  };
}

export function saveRecord<T>(
  resource: string,
  id: string | undefined,
  edits: Partial<T>,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "post" | "put" | "patch"> = apiClient,
  signal?: AbortSignal,
): Promise<T> {
  return id === undefined
    ? createResource<T>(resource, edits, registry, client, signal === undefined ? undefined : { signal })
    : updateResource<T>(
        resource,
        id,
        edits,
        undefined,
        registry,
        client,
        signal === undefined ? undefined : { signal },
      );
}

export function deleteRecord(
  resource: string,
  id: string,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "delete"> = apiClient,
): Promise<void> {
  return deleteResource(resource, id, registry, client);
}

export interface UseRecordOptions<R extends ResourceName = ResourceName> {
  // Fields whose remote change conflicts with a local edit; every field when unset.
  collaborationFields?: FieldOf<R>[];
  autoSave?: boolean;
  autoSaveDelay?: number;
  onRemoteChange?: (remoteRecord: RecordOf<R>, localRecord: RecordOf<R>) => void;
}

// The other users present on the record.
export type Collaborator<R extends ResourceName = ResourceName> = PresenceUser<R>;

export interface UseRecordResult<R extends ResourceName> {
  record: RecordOf<R> | undefined;
  localRecord: RecordOf<R> | undefined;
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;

  isUnavailable: boolean;
  unsavedEdits: Partial<UpdateOf<R>>;
  isDirty: boolean;
  setField: <K extends keyof UpdateOf<R>>(field: K, value: UpdateOf<R>[K]) => void;
  setFields: (updates: UpdateOf<R>) => void;

  save: () => Promise<void>;
  isSaving: boolean;
  saveError: AppError | null;

  discard: () => void;

  delete: () => Promise<void>;
  isDeleting: boolean;

  collaborators: Collaborator<R>[];
  hasConflict: boolean;
  resolveConflict: () => void;
}

function currentUserId(): string | null {
  return sessionIdentity(authMachine.getState())?.user.id ?? null;
}

export function useRecord<R extends ResourceName>(
  resource: R,
  id: string | undefined,
  options: UseRecordOptions<NoInfer<R>> = {},
): UseRecordResult<R> {
  type T = RecordOf<R>;
  const queryClient = useQueryClient();
  const availability = useRecordAvailability(resource, id);
  const { data, isLoading, isError, error } = useQuery({
    ...createRecordQueryOptions<T>(resource, id),
    enabled: id !== undefined && !availability.isUnavailable,
  });
  const [edits, setEdits] = useState<Partial<T>>({});
  const [hasConflict, setHasConflict] = useState(false);
  const presence = useRecordPresence(resource, id);
  const userId = useSyncExternalStore(authMachine.subscribe, currentUserId, () => null);
  const collaborators = useMemo(() => presence.filter((user) => user.userId !== userId), [presence, userId]);
  // The server version the local edits were last compared against.
  const base = useRef<{ resource: string; id: string | undefined; data: T | undefined }>({ resource, id, data });

  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
    setHasConflict(false);
  }, [resource, id]);

  const onServerVersion = useEffectEvent((previous: T, remote: T) => {
    const watched = options.collaborationFields as (keyof T)[] | undefined;
    const conflicted = (Object.keys(edits) as (keyof T)[]).some(
      (key) =>
        (watched === undefined || watched.includes(key)) &&
        !Object.is(remote[key], previous[key]) &&
        !Object.is(remote[key], edits[key]),
    );
    if (!conflicted) return;
    setHasConflict(true);
    options.onRemoteChange?.(remote, { ...remote, ...edits });
  });

  useEffect(() => {
    const previous = base.current;
    base.current = { resource, id, data };
    if (previous.resource !== resource || previous.id !== id) return;
    if (previous.data === undefined || data === undefined || previous.data === data) return;
    onServerVersion(previous.data, data);
  }, [resource, id, data]);

  const isDirty =
    data === undefined
      ? Object.keys(edits).length > 0
      : (Object.keys(edits) as (keyof T)[]).some((key) => !Object.is(edits[key], data[key]));
  const localRecord = !availability.isUnavailable && data ? { ...data, ...edits } : undefined;

  const saveMutation = useMutation({
    mutationFn: async (sentEdits: Partial<T>) => {
      availability.assertAvailable();
      const saved = await saveRecord<T>(resource, id, sentEdits, undefined, undefined, availability.signal);
      availability.assertAvailable();
      return saved;
    },
    onSuccess: (saved, sentEdits) => {
      if (!availability.isAvailable()) return;
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
      setHasConflict(false);
      // Preserve edits made while this request was in flight.
      setEdits((prev) => {
        const next = { ...prev };
        for (const key of Object.keys(sentEdits) as (keyof T)[]) {
          if (Object.is(next[key], sentEdits[key])) {
            delete next[key];
          }
        }
        return next;
      });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => {
      availability.assertAvailable();
      if (id === undefined) {
        return Promise.reject(new Error("useRecord: cannot delete a record with no id"));
      }
      return deleteRecord(resource, id);
    },
    onSuccess: () => {
      if (!availability.isAvailable()) return;
      queryClient.removeQueries({ queryKey: recordQueryKey(resource, id) });
    },
  });

  const setFields = (updates: UpdateOf<R>) => {
    if (!availability.isAvailable()) return;
    setEdits((prev) => ({ ...prev, ...(updates as Partial<T>) }));
  };

  const setField = <K extends keyof UpdateOf<R>>(field: K, value: UpdateOf<R>[K]) => {
    setFields({ [field]: value } as UpdateOf<R>);
  };

  const discard = () => {
    setEdits({});
    setHasConflict(false);
  };

  useEffect(() => {
    if (!options.autoSave || !isDirty || availability.isUnavailable) return;
    const timer = setTimeout(() => {
      if (availability.isAvailable()) saveMutation.mutate(edits);
    }, options.autoSaveDelay ?? 2000);
    return () => clearTimeout(timer);
  }, [
    edits,
    isDirty,
    options.autoSave,
    options.autoSaveDelay,
    saveMutation.mutate,
    availability.isUnavailable,
    availability.isAvailable,
  ]);

  return {
    record: availability.isUnavailable ? undefined : data,
    localRecord,
    isLoading: !availability.isUnavailable && isLoading,
    isUnavailable: availability.isUnavailable,
    unsavedEdits: edits as Partial<UpdateOf<R>>,
    isError,
    error: error as AppError | null,
    isDirty,
    setField,
    setFields,
    save: async () => {
      availability.assertAvailable();
      await saveMutation.mutateAsync(edits);
    },
    isSaving: !availability.isUnavailable && saveMutation.isPending,
    saveError: availability.isUnavailable ? null : (saveMutation.error as AppError | null),
    discard,
    delete: async () => {
      await deleteMutation.mutateAsync();
    },
    isDeleting: !availability.isUnavailable && deleteMutation.isPending,
    collaborators,
    hasConflict: !availability.isUnavailable && hasConflict,
    resolveConflict: discard,
  };
}
