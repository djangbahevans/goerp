import { keepPreviousData, type QueryKey, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";

// typescript-sdk-reference.md's useRecordReaders — the active tenant
// members who can read a record, matching a search, for a per-record user
// picker (the chatter's mention autocomplete, a scheduled activity's
// assignee picker). Backed by the built-in /_meta/record-readers endpoint
// (record-activity.md §6). Failures reject with an AppError and show no
// toast.
export interface RecordReader {
  id: string;
  name: string | null;
  email: string;
  avatarUrl: string | null;
}

export interface UseRecordReadersOptions {
  // Leaves the caller out, as a mention list does. Defaults to false.
  excludeSelf?: boolean;
  // 1–20; the engine defaults to 8.
  limit?: number;
}

export interface UseRecordReadersResult {
  // Ordered by name.
  readers: RecordReader[];
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
}

interface RecordReaderWire {
  id: string;
  name: string | null;
  email: string;
  avatar_url: string | null;
}

// Matches RelationPicker's search debounce.
const DEBOUNCE_MS = 300;
const STALE_MS = 30_000;

// Every option is part of the key, so a mention list and an assignee
// picker on the same record never share an entry.
export function recordReadersQueryKey(
  model: string,
  recordId: string,
  query: string,
  options: UseRecordReadersOptions = {},
): QueryKey {
  return ["record-readers", model, recordId, query, options.excludeSelf ?? false, options.limit ?? null];
}

export function createRecordReadersQueryOptions(
  model: string,
  recordId: string,
  query: string,
  options: UseRecordReadersOptions = {},
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordReadersQueryKey(model, recordId, query, options),
    queryFn: async (): Promise<RecordReader[]> => {
      const wire = await client.get<{ data: RecordReaderWire[] }>("/_meta/record-readers", {
        params: {
          model,
          record_id: recordId,
          q: query,
          ...(options.excludeSelf ? { exclude_self: true } : {}),
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
        },
      });
      return wire.data.map((r) => ({ id: r.id, name: r.name, email: r.email, avatarUrl: r.avatar_url }));
    },
    staleTime: STALE_MS,
  };
}

// null passes through at once, so disabling never waits and re-enabling
// never fetches a stale pre-null query.
function useDebouncedQuery(query: string | null): string | null {
  const [debounced, setDebounced] = useState(query);
  useEffect(() => {
    if (query === null) {
      setDebounced(null);
      return;
    }
    const timeout = setTimeout(() => setDebounced(query), DEBOUNCE_MS);
    return () => clearTimeout(timeout);
  }, [query]);
  return query === null ? null : debounced;
}

// The search text is the key's fourth element.
function sameExceptQuery(a: QueryKey, b: QueryKey): boolean {
  return a.length === b.length && a.every((part, i) => i === 3 || part === b[i]);
}

export function useRecordReaders(
  model: string,
  recordId: string,
  query: string | null,
  options: UseRecordReadersOptions = {},
): UseRecordReadersResult {
  const debounced = useDebouncedQuery(query);
  const enabled = debounced !== null;
  const result = useQuery({
    ...createRecordReadersQueryOptions(model, recordId, debounced ?? "", options),
    enabled,
    // Keeps the previous keystroke's list while the next loads, so the
    // list doesn't flash empty — but never another record's readers.
    placeholderData: (previous, previousQuery) =>
      previousQuery !== undefined &&
      sameExceptQuery(previousQuery.queryKey, recordReadersQueryKey(model, recordId, "", options))
        ? keepPreviousData(previous)
        : undefined,
  });

  if (query === null) {
    return { readers: [], isLoading: false, isError: false, error: null };
  }
  return {
    readers: result.data ?? [],
    isLoading: debounced !== query || result.isPending || result.isPlaceholderData,
    isError: result.isError,
    error: result.error as AppError | null,
  };
}
