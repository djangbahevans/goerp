import type { APIClient } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import {
  createRecordQueryOptions as createSDKRecordQueryOptions,
  invalidateResourceLists,
  recordQueryKey,
  saveRecord as saveSDKRecord,
  useRecordAvailability,
  useRecordPreview,
} from "@goerp/sdk/react";
import type { ModelDef, ModelRegistry, ResourceRegistry } from "@goerp/sdk/schema";
import { modelRegistry, resourceRegistry } from "@goerp/sdk/schema";
import { type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useRef, useState } from "react";
import type { Row } from "../list/list-view-types.js";

export { recordQueryKey } from "@goerp/sdk/react";

export function createRecordQueryOptions(
  resource: string,
  id: string | undefined,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    ...createSDKRecordQueryOptions<Row>(resource, id, registry, client),
    queryKey: recordQueryKey(resource, id),
  };
}

export function saveRecord(
  resource: string,
  id: string | undefined,
  edits: Row,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "post" | "put" | "patch"> = apiClient,
  signal?: AbortSignal,
): Promise<Row> {
  return saveSDKRecord<Row>(resource, id, edits, registry, client, signal);
}

export function createFormDefaults(model: ModelDef): Row {
  const defaults: Row = {};
  for (const field of model.fields) {
    if (field.default !== undefined && !field.readonly && !field.primary_key) defaults[field.name] = field.default;
  }
  return defaults;
}

export function modelDefaultsQueryKey(resource: string): QueryKey {
  return ["form-model-defaults", resource];
}

export interface UseFormRecordOptions {
  autoSave?: boolean;
  autoSaveDelay?: number;
  onSaved?: (record: Row) => void;
  registry?: Pick<ResourceRegistry, "resolve">;
  client?: Pick<APIClient, "get" | "post" | "put" | "patch">;
  models?: Pick<ModelRegistry, "resolve">;
}

export interface FormRecordHandle {
  record: Row;
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => void;
  isUnavailable: boolean;
  unsavedEdits: Row;
  isDirty: boolean;
  isPreviewing: boolean;
  setField: (patch: Record<string, unknown>) => void;
  reset: () => void;
  // Fire-and-forget: a failure lands in saveError, never as a rejection.
  save: () => void;
  isSaving: boolean;
  saveError: Error | null;
  hasConflict: boolean;
  keepLocalEdits: () => void;
  resolveConflict: () => void;
}

export function useFormRecord(
  resource: string,
  id: string | undefined,
  options: UseFormRecordOptions = {},
): FormRecordHandle {
  const queryClient = useQueryClient();
  const availability = useRecordAvailability(resource, id);
  const { data, isLoading, isError, error, refetch } = useQuery({
    ...createRecordQueryOptions(resource, id, options.registry, options.client),
    enabled: id !== undefined && !availability.isUnavailable,
  });
  const isCreate = id === undefined;
  const defaults = useQuery({
    queryKey: modelDefaultsQueryKey(resource),
    queryFn: async () => createFormDefaults(await (options.models ?? modelRegistry).resolve(resource)),
    enabled: isCreate,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });
  const [edits, setEdits] = useState<Row>({});
  const preview = useRecordPreview<Row>(resource, id, {
    record: isCreate ? defaults.data : data,
    edits,
    availability,
    models: options.models,
    registry: options.registry,
    client: options.client,
  });
  const [hasConflict, setHasConflict] = useState(false);
  // The server version the local edits were last compared against.
  const base = useRef<{ resource: string; id: string | undefined; data: Row | undefined }>({ resource, id, data });
  const record = availability.isUnavailable
    ? {}
    : { ...(isCreate ? defaults.data : undefined), ...(data ?? {}), ...preview.values, ...edits };
  const isDirty = Object.keys(edits).length > 0;

  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
    setHasConflict(false);
  }, [resource, id]);

  const onServerVersion = useEffectEvent((previous: Row, remote: Row) => {
    const conflicted = Object.keys(edits).some(
      (key) => !Object.is(remote[key], previous[key]) && !Object.is(remote[key], edits[key]),
    );
    if (conflicted) setHasConflict(true);
  });

  useEffect(() => {
    const previous = base.current;
    base.current = { resource, id, data };
    if (previous.resource !== resource || previous.id !== id) return;
    if (previous.data === undefined || data === undefined || previous.data === data) return;
    onServerVersion(previous.data, data);
  }, [resource, id, data]);

  const mutation = useMutation({
    mutationFn: async () => {
      availability.assertAvailable();
      const saved = await saveRecord(resource, id, edits, options.registry, options.client, availability.signal);
      availability.assertAvailable();
      return saved;
    },
    onSuccess: (saved) => {
      void invalidateResourceLists(queryClient, resource);
      if (!availability.isAvailable()) return;
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
      preview.reset();
      setEdits({});
      setHasConflict(false);
      options.onSaved?.(saved);
    },
  });

  const setField = (patch: Record<string, unknown>) => {
    if (!availability.isAvailable()) return;
    preview.invalidate();
    setEdits((prev) => ({ ...prev, ...patch }));
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: edits re-arms autosave after each local change.
  useEffect(() => {
    if (!options.autoSave || !isDirty || availability.isUnavailable) return;
    const timer = setTimeout(() => {
      if (availability.isAvailable()) mutation.mutate();
    }, options.autoSaveDelay ?? 2000);
    return () => clearTimeout(timer);
  }, [
    edits,
    isDirty,
    options.autoSave,
    options.autoSaveDelay,
    mutation.mutate,
    availability.isUnavailable,
    availability.isAvailable,
  ]);

  return {
    record,
    isLoading: !availability.isUnavailable && (isLoading || (isCreate && defaults.isLoading)),
    isUnavailable: availability.isUnavailable,
    unsavedEdits: edits,
    isError,
    error: error as Error | null,
    refetch: () => {
      if (availability.isAvailable()) void refetch();
    },
    isDirty,
    isPreviewing: preview.isPreviewing,
    setField,
    reset: () => {
      preview.reset();
      setEdits({});
      setHasConflict(false);
      mutation.reset();
    },
    save: () => {
      if (availability.isAvailable()) mutation.mutate();
    },
    isSaving: !availability.isUnavailable && mutation.isPending,
    saveError: availability.isUnavailable ? null : (mutation.error as Error | null),
    hasConflict: !availability.isUnavailable && hasConflict,
    keepLocalEdits: () => setHasConflict(false),
    resolveConflict: () => {
      preview.reset();
      setEdits({});
      setHasConflict(false);
    },
  };
}
