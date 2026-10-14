import type { APIClient } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import {
  createRecordQueryOptions as createSDKRecordQueryOptions,
  recordQueryKey,
  saveRecord as saveSDKRecord,
  useRecordAvailability,
} from "@goerp/sdk/react";
import type { ModelDef, ModelRegistry, ResourceRegistry } from "@goerp/sdk/schema";
import { modelRegistry, resourceRegistry } from "@goerp/sdk/schema";
import { type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
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

// What an empty create form starts from: the literal default of each field the server fills in on
// create. A readonly or primary-key field is set by the engine and never shown as a choice.
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
  setField: (patch: Record<string, unknown>) => void;
  reset: () => void;
  // Fire-and-forget: a failure lands in saveError, never as a rejection.
  save: () => void;
  isSaving: boolean;
  saveError: Error | null;
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
  const record = availability.isUnavailable
    ? {}
    : { ...(isCreate ? defaults.data : undefined), ...(data ?? {}), ...edits };
  const isDirty = Object.keys(edits).length > 0;

  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
  }, [resource, id]);

  const mutation = useMutation({
    mutationFn: async () => {
      availability.assertAvailable();
      const saved = await saveRecord(resource, id, edits, options.registry, options.client, availability.signal);
      availability.assertAvailable();
      return saved;
    },
    onSuccess: (saved) => {
      if (!availability.isAvailable()) return;
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
      setEdits({});
      options.onSaved?.(saved);
    },
  });

  const setField = (patch: Record<string, unknown>) => {
    if (!availability.isAvailable()) return;
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
    setField,
    reset: () => {
      setEdits({});
      mutation.reset();
    },
    save: () => {
      if (availability.isAvailable()) mutation.mutate();
    },
    isSaving: !availability.isUnavailable && mutation.isPending,
    saveError: availability.isUnavailable ? null : (mutation.error as Error | null),
  };
}
