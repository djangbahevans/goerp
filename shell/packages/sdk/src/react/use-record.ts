import { type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

// typescript-sdk-reference.md §5 `useRecord` — scoped to load/edit/save/
// delete (goerp#650). collaborationFields/onRemoteChange/resolveConflict/
// collaborators need a presence system that doesn't exist yet (backlog
// #510, unfiled); live-recompute (`preview`) integration is its own
// ticket (backlog #499/#757). Both are left off this return shape
// entirely rather than stubbed with fake always-empty values.

function fillId(path: string, id: string): string {
  return path.replace("{id}", id);
}

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
    queryFn: async (): Promise<T> => {
      const entry = await registry.resolve(resource);
      return client.get<T>(fillId(entry.getPath, id as string));
    },
    enabled: id !== undefined,
  };
}

// POSTs to createPath with no id, otherwise dispatches to the model's own
// declared update method (PUT/PATCH) against the id-filled updatePath.
// Rejects rather than silently dispatching to an empty path when the model
// declares no create/update route (buildResourceRegistry's "" default —
// resource-registry.ts).
export async function saveRecord<T>(
  resource: string,
  id: string | undefined,
  edits: Partial<T>,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "post" | "put" | "patch"> = apiClient,
): Promise<T> {
  const entry = await registry.resolve(resource);
  if (id === undefined) {
    if (entry.createPath === "") {
      throw new Error(`useRecord: resource "${resource}" declares no create route`);
    }
    return client.post<T>(entry.createPath, edits);
  }
  if (entry.updatePath === "") {
    throw new Error(`useRecord: resource "${resource}" declares no update route`);
  }
  const path = fillId(entry.updatePath, id);
  return entry.updateMethod === "PATCH" ? client.patch<T>(path, edits) : client.put<T>(path, edits);
}

// Rejects rather than silently no-op-ing when the model declares no
// delete route (ResourceRegistryEntry.deletePath === null).
export async function deleteRecord(
  resource: string,
  id: string,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "delete"> = apiClient,
): Promise<void> {
  const entry = await registry.resolve(resource);
  if (entry.deletePath === null) {
    throw new Error(`useRecord: resource "${resource}" declares no delete route`);
  }
  await client.delete(fillId(entry.deletePath, id));
}

export interface UseRecordOptions {
  autoSave?: boolean;
  autoSaveDelay?: number;
}

export interface UseRecordResult<T> {
  record: T | undefined;
  localRecord: T | undefined;
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;

  isDirty: boolean;
  setField: (field: keyof T, value: unknown) => void;
  setFields: (updates: Partial<T>) => void;

  save: () => Promise<void>;
  isSaving: boolean;
  saveError: AppError | null;

  discard: () => void;

  delete: () => Promise<void>;
  isDeleting: boolean;
}

export function useRecord<T extends Record<string, unknown>>(
  resource: string,
  id: string | undefined,
  options: UseRecordOptions = {},
): UseRecordResult<T> {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error } = useQuery(createRecordQueryOptions<T>(resource, id));
  const [edits, setEdits] = useState<Partial<T>>({});

  // Don't carry edits from a previous record over if id/resource changes
  // without a remount.
  // biome-ignore lint/correctness/useExhaustiveDependencies: resource/id are this hook's own params, not component state — the rule doesn't track them as reactive.
  useEffect(() => {
    setEdits({});
  }, [resource, id]);

  // Compares against the loaded record, not just edits' key presence — a
  // field edited back to its original value must not read as dirty.
  // Before the record has loaded (create mode), any edit is dirty since
  // there's no baseline to compare against.
  const isDirty =
    data === undefined
      ? Object.keys(edits).length > 0
      : (Object.keys(edits) as (keyof T)[]).some((key) => !Object.is(edits[key], data[key]));
  const localRecord = data ? { ...data, ...edits } : undefined;

  const saveMutation = useMutation({
    mutationFn: (sentEdits: Partial<T>) => saveRecord<T>(resource, id, sentEdits),
    onSuccess: (saved, sentEdits) => {
      queryClient.setQueryData(recordQueryKey(resource, id), saved);
      // Only clear the keys this save actually sent, and only where they
      // still hold the sent value — an edit made while the save was in
      // flight (not part of `sentEdits`) must survive, not be wiped by a
      // response that raced it.
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

  const setFields = (updates: Partial<T>) => {
    setEdits((prev) => ({ ...prev, ...updates }));
  };

  const setField = (field: keyof T, value: unknown) => {
    setFields({ [field]: value } as Partial<T>);
  };

  const discard = () => {
    setEdits({});
  };

  // Debounced autosave, reset on every edit while dirty. mutateAsync is a
  // stable bound reference (MutationObserver binds it once in its own
  // constructor), so calling it directly here needs no ref.
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
