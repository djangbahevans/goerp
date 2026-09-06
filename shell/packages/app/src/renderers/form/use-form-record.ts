import type { APIClient } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import type { ResourceRegistry } from "@goerp/sdk/schema";
import { resourceRegistry } from "@goerp/sdk/schema";
import { type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Row } from "../list/list-view-types.js";

// A narrow, form-scoped load/save hook — not the full useRecord (goerp#650,
// which also needs a presence system that doesn't exist yet).

function fillId(path: string, id: string): string {
  return path.replace("{id}", id);
}

export function recordQueryKey(resource: string, id: string | undefined): QueryKey {
  return ["form-record", resource, id ?? null];
}

export function createRecordQueryOptions(
  resource: string,
  id: string | undefined,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordQueryKey(resource, id),
    queryFn: async (): Promise<Row> => {
      const entry = await registry.resolve(resource);
      return client.get<Row>(fillId(entry.getPath, id as string));
    },
    enabled: id !== undefined,
  };
}

// POSTs to createPath with no id, otherwise dispatches to the model's own
// declared update method (PUT/PATCH) against the id-filled updatePath.
export async function saveRecord(
  resource: string,
  id: string | undefined,
  edits: Row,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "post" | "put" | "patch"> = apiClient,
): Promise<Row> {
  const entry = await registry.resolve(resource);
  if (id === undefined) {
    return client.post<Row>(entry.createPath, edits);
  }
  const path = fillId(entry.updatePath, id);
  return entry.updateMethod === "PATCH" ? client.patch<Row>(path, edits) : client.put<Row>(path, edits);
}

export interface UseFormRecordOptions {
  autoSave?: boolean;
  autoSaveDelay?: number;
  onSaved?: (record: Row) => void;
}

export interface FormRecordHandle {
  record: Row;
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => void;
  isDirty: boolean;
  setField: (patch: Record<string, unknown>) => void;
  save: () => Promise<void>;
  isSaving: boolean;
  saveError: Error | null;
}

export function useFormRecord(
  resource: string,
  id: string | undefined,
  options: UseFormRecordOptions = {},
): FormRecordHandle {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error, refetch } = useQuery(createRecordQueryOptions(resource, id));
  const [edits, setEdits] = useState<Row>({});
  const record = { ...(data ?? {}), ...edits };
  const isDirty = Object.keys(edits).length > 0;

  // Don't carry edits from a previous record over if id/resource changes
  // without a remount.
  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
  }, [resource, id]);

  const mutation = useMutation({
    mutationFn: () => saveRecord(resource, id, edits),
    onSuccess: (saved) => {
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
      setEdits({});
      options.onSaved?.(saved);
    },
  });

  const setField = (patch: Record<string, unknown>) => {
    setEdits((prev) => ({ ...prev, ...patch }));
  };

  // Debounced autosave, reset on every edit while dirty. mutateAsync is a
  // stable bound reference (MutationObserver binds it once in its own
  // constructor), so calling it directly here needs no ref.
  // biome-ignore lint/correctness/useExhaustiveDependencies: `edits` re-arms the timer; not read directly in the body.
  useEffect(() => {
    if (!options.autoSave || !isDirty) return;
    const timer = setTimeout(() => {
      void mutation.mutateAsync();
    }, options.autoSaveDelay ?? 2000);
    return () => clearTimeout(timer);
  }, [edits, isDirty, options.autoSave, options.autoSaveDelay, mutation.mutateAsync]);

  return {
    record,
    isLoading,
    isError,
    error: error as Error | null,
    refetch: () => void refetch(),
    isDirty,
    setField,
    save: async () => {
      await mutation.mutateAsync();
    },
    isSaving: mutation.isPending,
    saveError: mutation.error as Error | null,
  };
}
