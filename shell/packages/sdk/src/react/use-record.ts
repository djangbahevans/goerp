import { type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { createResource, deleteResource, getResource, updateResource } from "../api/resource-api.js";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { RecordOf, ResourceName, UpdateOf } from "../names.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

export function recordQueryKey(resource: string, id: string | undefined): QueryKey {
  return ["record", resource, id ?? null];
}

export function createRecordQueryOptions<T>(
  resource: string,
  id: string | undefined,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordQueryKey(resource, id),
    queryFn: (): Promise<T> => getResource<T>(resource, id as string, registry, client),
    enabled: id !== undefined,
  };
}

export function saveRecord<T>(
  resource: string,
  id: string | undefined,
  edits: Partial<T>,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "post" | "put" | "patch"> = apiClient,
): Promise<T> {
  return id === undefined
    ? createResource<T>(resource, edits, registry, client)
    : updateResource<T>(resource, id, edits, undefined, registry, client);
}

export function deleteRecord(
  resource: string,
  id: string,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "delete"> = apiClient,
): Promise<void> {
  return deleteResource(resource, id, registry, client);
}

export interface UseRecordOptions<_R extends ResourceName = ResourceName> {
  autoSave?: boolean;
  autoSaveDelay?: number;
}

export interface UseRecordResult<R extends ResourceName> {
  record: RecordOf<R> | undefined;
  localRecord: RecordOf<R> | undefined;
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;

  isDirty: boolean;
  setField: <K extends keyof UpdateOf<R>>(field: K, value: UpdateOf<R>[K]) => void;
  setFields: (updates: UpdateOf<R>) => void;

  save: () => Promise<void>;
  isSaving: boolean;
  saveError: AppError | null;

  discard: () => void;

  delete: () => Promise<void>;
  isDeleting: boolean;
}

export function useRecord<R extends ResourceName>(
  resource: R,
  id: string | undefined,
  options: UseRecordOptions<NoInfer<R>> = {},
): UseRecordResult<R> {
  type T = RecordOf<R>;
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error } = useQuery(createRecordQueryOptions<T>(resource, id));
  const [edits, setEdits] = useState<Partial<T>>({});

  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
  }, [resource, id]);

  const isDirty =
    data === undefined
      ? Object.keys(edits).length > 0
      : (Object.keys(edits) as (keyof T)[]).some((key) => !Object.is(edits[key], data[key]));
  const localRecord = data ? { ...data, ...edits } : undefined;

  const saveMutation = useMutation({
    mutationFn: (sentEdits: Partial<T>) => saveRecord<T>(resource, id, sentEdits),
    onSuccess: (saved, sentEdits) => {
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
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
      if (id === undefined) {
        return Promise.reject(new Error("useRecord: cannot delete a record with no id"));
      }
      return deleteRecord(resource, id);
    },
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: recordQueryKey(resource, id) });
    },
  });

  const setFields = (updates: UpdateOf<R>) => {
    setEdits((prev) => ({ ...prev, ...(updates as Partial<T>) }));
  };

  const setField = <K extends keyof UpdateOf<R>>(field: K, value: UpdateOf<R>[K]) => {
    setFields({ [field]: value } as UpdateOf<R>);
  };

  const discard = () => {
    setEdits({});
  };

  useEffect(() => {
    if (!options.autoSave || !isDirty) return;
    const timer = setTimeout(() => {
      void saveMutation.mutateAsync(edits);
    }, options.autoSaveDelay ?? 2000);
    return () => clearTimeout(timer);
  }, [edits, isDirty, options.autoSave, options.autoSaveDelay, saveMutation.mutateAsync]);

  return {
    record: data,
    localRecord,
    isLoading,
    isError,
    error: error as AppError | null,
    isDirty,
    setField,
    setFields,
    save: async () => {
      await saveMutation.mutateAsync(edits);
    },
    isSaving: saveMutation.isPending,
    saveError: saveMutation.error as AppError | null,
    discard,
    delete: async () => {
      await deleteMutation.mutateAsync();
    },
    isDeleting: deleteMutation.isPending,
  };
}
