import {
  type InfiniteData,
  type QueryClient,
  type QueryKey,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { useState } from "react";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { type PagedResponseWire, toPagedResponse } from "../http/paged-response.js";
import type { APIClient, PagedResponse } from "../http/types.js";

// typescript-sdk-reference.md's useRecordActivity — one record's activity
// feed, backed by the built-in /_meta/activity endpoint (record-activity.md
// §6), not a module route. Failures reject with an AppError carrying the
// server's code and show no toast.
export type ActivityKind = "created" | "change" | "comment" | "activity_done";

export interface ActivityAuthor {
  id: string;
  name: string | null;
  avatarUrl: string | null;
}

// Values are raw, in the shape host.orm.read returns for the field.
export interface ActivityFieldChange {
  field: string;
  old: unknown;
  new: unknown;
}

export interface CompletedActivity {
  activityId: string;
  type: string;
  summary: string;
  dueDate: string;
  feedback: string | null;
}

interface ActivityEntryBase {
  id: string;
  // null for a change made with no user in context (a system change).
  author: ActivityAuthor | null;
  createdAt: string;
}

export type ActivityEntry =
  | (ActivityEntryBase & { kind: "created" })
  | (ActivityEntryBase & { kind: "change"; changes: ActivityFieldChange[] })
  | (ActivityEntryBase & { kind: "comment"; body: string | null; deleted: boolean })
  | (ActivityEntryBase & { kind: "activity_done"; activity: CompletedActivity });

export interface UseRecordActivityOptions {
  limit?: number;
}

export interface UseRecordActivityResult {
  entries: ActivityEntry[];
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
  hasMore: boolean;
  fetchMore: () => void;
  isFetchingNextPage: boolean;
  refetch: () => void;
  postComment: (body: string) => Promise<ActivityEntry>;
  isPosting: boolean;
  deleteComment: (id: string) => Promise<void>;
  // Every comment with a delete in flight; concurrent deletes are each tracked.
  deletingIds: readonly string[];
}

interface ActivityAuthorWire {
  id: string;
  name: string | null;
  avatar_url: string | null;
}

interface ActivityEntryWire {
  id: string;
  kind: ActivityKind;
  body?: string;
  deleted?: boolean;
  changes?: ActivityFieldChange[];
  activity?: { activity_id: string; type: string; summary: string; due_date: string; feedback: string | null };
  author: ActivityAuthorWire | null;
  created_at: string;
}

function toActivityEntry(wire: ActivityEntryWire): ActivityEntry {
  const base: ActivityEntryBase = {
    id: wire.id,
    author: wire.author ? { id: wire.author.id, name: wire.author.name, avatarUrl: wire.author.avatar_url } : null,
    createdAt: wire.created_at,
  };
  switch (wire.kind) {
    case "change":
      return { ...base, kind: "change", changes: wire.changes ?? [] };
    case "comment":
      return { ...base, kind: "comment", body: wire.body ?? null, deleted: wire.deleted ?? false };
    case "activity_done": {
      const a = wire.activity;
      return {
        ...base,
        kind: "activity_done",
        activity: {
          activityId: a?.activity_id ?? "",
          type: a?.type ?? "",
          summary: a?.summary ?? "",
          dueDate: a?.due_date ?? "",
          feedback: a?.feedback ?? null,
        },
      };
    }
    default:
      return { ...base, kind: "created" };
  }
}

export function recordActivityQueryKey(model: string, recordId: string): QueryKey {
  return ["record-activity", model, recordId];
}

export function createRecordActivityQueryOptions(
  model: string,
  recordId: string,
  options: UseRecordActivityOptions = {},
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: [...recordActivityQueryKey(model, recordId), options.limit ?? null] as QueryKey,
    queryFn: async ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<ActivityEntry>> => {
      const wire = await client.get<PagedResponseWire<ActivityEntryWire>>("/_meta/activity", {
        params: {
          model,
          record_id: recordId,
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
      });
      return toPagedResponse(wire, toActivityEntry);
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: PagedResponse<ActivityEntry>) =>
      lastPage.meta.hasMore ? (lastPage.meta.cursor ?? undefined) : undefined,
  };
}

// Refetching an infinite query reloads every page it holds from the first,
// recomputing cursors, so the feed stays gap- and duplicate-free after a
// post or delete shifts entries across page boundaries.
function refetchFeed(queryClient: QueryClient, model: string, recordId: string) {
  return queryClient.invalidateQueries({ queryKey: recordActivityQueryKey(model, recordId) });
}

export function createPostCommentMutationOptions(
  queryClient: QueryClient,
  model: string,
  recordId: string,
  client: Pick<APIClient, "post"> = apiClient,
) {
  return {
    mutationFn: async (body: string): Promise<ActivityEntry> =>
      toActivityEntry(await client.post<ActivityEntryWire>("/_meta/activity", { model, record_id: recordId, body })),
    onSuccess: () => refetchFeed(queryClient, model, recordId),
  };
}

export function createDeleteCommentMutationOptions(
  queryClient: QueryClient,
  model: string,
  recordId: string,
  client: Pick<APIClient, "delete"> = apiClient,
) {
  return {
    mutationFn: (id: string) => client.delete<void>(`/_meta/activity/${id}`),
    onSuccess: () => refetchFeed(queryClient, model, recordId),
  };
}

export function useRecordActivity(
  model: string,
  recordId: string,
  options: UseRecordActivityOptions = {},
): UseRecordActivityResult {
  const queryClient = useQueryClient();
  const query = useInfiniteQuery<
    PagedResponse<ActivityEntry>,
    AppError,
    InfiniteData<PagedResponse<ActivityEntry>>,
    QueryKey,
    string | undefined
  >(createRecordActivityQueryOptions(model, recordId, options));
  const postMutation = useMutation(createPostCommentMutationOptions(queryClient, model, recordId));
  const deleteMutation = useMutation(createDeleteCommentMutationOptions(queryClient, model, recordId));
  const [deletingIds, setDeletingIds] = useState<readonly string[]>([]);

  return {
    entries: query.data?.pages.flatMap((page) => page.data) ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    hasMore: query.hasNextPage,
    fetchMore: () => {
      void query.fetchNextPage();
    },
    isFetchingNextPage: query.isFetchingNextPage,
    refetch: () => void query.refetch(),
    postComment: (body) => postMutation.mutateAsync(body),
    isPosting: postMutation.isPending,
    deleteComment: async (id) => {
      setDeletingIds((current) => [...current, id]);
      try {
        await deleteMutation.mutateAsync(id);
      } finally {
        setDeletingIds((current) => current.filter((deleting) => deleting !== id));
      }
    },
    deletingIds,
  };
}
