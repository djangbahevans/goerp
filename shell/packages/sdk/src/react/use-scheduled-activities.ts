import {
  type InfiniteData,
  type QueryClient,
  type QueryKey,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useState } from "react";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { type PagedResponseWire, toPagedResponse } from "../http/paged-response.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import { type ActivityAuthor, recordActivityQueryKey } from "./use-record-activity.js";

// typescript-sdk-reference.md's useScheduledActivities and useMyActivities —
// planned calls, meetings, emails and to-dos on a record, backed by the
// built-in /_meta/scheduled-activities endpoint (scheduled-activities.md §5).
// Failures reject with an AppError carrying the server's code and show no
// toast.
export interface ScheduledActivity {
  id: string;
  model: string;
  recordId: string;
  type: string;
  summary: string;
  note: string | null;
  dueDate: string;
  assignee: ActivityAuthor;
  createdBy: ActivityAuthor;
  createdAt: string;
  doneAt: string | null;
  doneBy: ActivityAuthor | null;
  feedback: string | null;
}

export interface MyScheduledActivity extends ScheduledActivity {
  // The record's display name from the caller's own read.
  recordName: string | null;
}

export interface ScheduleActivityInput {
  type: string;
  summary: string;
  dueDate: string;
  note?: string;
  // Defaults to the caller.
  assigneeId?: string;
}

// An absent field is left unchanged; note: null clears the note.
export interface UpdateScheduledActivityInput {
  type?: string;
  summary?: string;
  note?: string | null;
  dueDate?: string;
  assigneeId?: string;
}

export interface MarkActivityDoneInput {
  feedback?: string;
}

export interface ScheduledActivityChanges {
  update: (id: string, changes: UpdateScheduledActivityInput) => Promise<ScheduledActivity>;
  markDone: (id: string, input?: MarkActivityDoneInput) => Promise<ScheduledActivity>;
  cancel: (id: string) => Promise<void>;
  // Every activity with an update, completion or cancel in flight.
  pendingIds: readonly string[];
}

export interface UseScheduledActivitiesResult extends ScheduledActivityChanges {
  activities: ScheduledActivity[];
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
  refetch: () => void;
  schedule: (input: ScheduleActivityInput) => Promise<ScheduledActivity>;
  isScheduling: boolean;
}

export interface UseMyActivitiesOptions {
  limit?: number;
}

export interface UseMyActivitiesResult extends ScheduledActivityChanges {
  activities: MyScheduledActivity[];
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
  hasMore: boolean;
  fetchMore: () => void;
  isFetchingNextPage: boolean;
  refetch: () => void;
}

interface ActivityUserWire {
  id: string;
  name: string | null;
  avatar_url: string | null;
}

interface ScheduledActivityWire {
  id: string;
  model: string;
  record_id: string;
  type: string;
  summary: string;
  note: string | null;
  due_date: string;
  assignee: ActivityUserWire;
  created_by: ActivityUserWire;
  created_at: string;
  done_at: string | null;
  done_by: ActivityUserWire | null;
  feedback: string | null;
}

interface MyScheduledActivityWire extends ScheduledActivityWire {
  record_name: string | null;
}

function toUser(wire: ActivityUserWire): ActivityAuthor {
  return { id: wire.id, name: wire.name, avatarUrl: wire.avatar_url };
}

function toScheduledActivity(wire: ScheduledActivityWire): ScheduledActivity {
  return {
    id: wire.id,
    model: wire.model,
    recordId: wire.record_id,
    type: wire.type,
    summary: wire.summary,
    note: wire.note,
    dueDate: wire.due_date,
    assignee: toUser(wire.assignee),
    createdBy: toUser(wire.created_by),
    createdAt: wire.created_at,
    doneAt: wire.done_at,
    doneBy: wire.done_by ? toUser(wire.done_by) : null,
    feedback: wire.feedback,
  };
}

function toMyScheduledActivity(wire: MyScheduledActivityWire): MyScheduledActivity {
  return { ...toScheduledActivity(wire), recordName: wire.record_name };
}

const basePath = "/_meta/scheduled-activities";

// Every scheduled-activity list shares this prefix, so one invalidation
// refreshes a record's list and "My activities" alike.
export const scheduledActivitiesQueryKey: QueryKey = ["scheduled-activities"];

export function recordScheduledActivitiesQueryKey(model: string, recordId: string): QueryKey {
  return [...scheduledActivitiesQueryKey, "record", model, recordId];
}

export function myScheduledActivitiesQueryKey(): QueryKey {
  return [...scheduledActivitiesQueryKey, "mine"];
}

export function createScheduledActivitiesQueryOptions(
  model: string,
  recordId: string,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordScheduledActivitiesQueryKey(model, recordId),
    queryFn: async (): Promise<ScheduledActivity[]> => {
      const wire = await client.get<{ data: ScheduledActivityWire[] }>(basePath, {
        params: { model, record_id: recordId },
      });
      return wire.data.map(toScheduledActivity);
    },
  };
}

export function createMyActivitiesQueryOptions(
  options: UseMyActivitiesOptions = {},
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: [...myScheduledActivitiesQueryKey(), options.limit ?? null] as QueryKey,
    queryFn: async ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<MyScheduledActivity>> => {
      const wire = await client.get<PagedResponseWire<MyScheduledActivityWire>>(`${basePath}/mine`, {
        params: {
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
      });
      return toPagedResponse(wire, toMyScheduledActivity);
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: PagedResponse<MyScheduledActivity>) =>
      lastPage.meta.hasMore ? (lastPage.meta.cursor ?? undefined) : undefined,
  };
}

// Refetching reloads every page an infinite query holds from the first, so
// "My activities" stays gap- and duplicate-free after a change moves or
// removes an activity.
function refetchActivities(queryClient: QueryClient) {
  return queryClient.invalidateQueries({ queryKey: scheduledActivitiesQueryKey });
}

export function createScheduleActivityMutationOptions(
  queryClient: QueryClient,
  model: string,
  recordId: string,
  client: Pick<APIClient, "post"> = apiClient,
) {
  return {
    mutationFn: async (input: ScheduleActivityInput): Promise<ScheduledActivity> =>
      toScheduledActivity(
        await client.post<ScheduledActivityWire>(basePath, {
          model,
          record_id: recordId,
          type: input.type,
          summary: input.summary,
          due_date: input.dueDate,
          ...(input.note !== undefined ? { note: input.note } : {}),
          ...(input.assigneeId !== undefined ? { assignee_id: input.assigneeId } : {}),
        }),
      ),
    onSuccess: () => refetchActivities(queryClient),
  };
}

export function createUpdateActivityMutationOptions(
  queryClient: QueryClient,
  client: Pick<APIClient, "patch"> = apiClient,
) {
  return {
    mutationFn: async ({
      id,
      changes,
    }: {
      id: string;
      changes: UpdateScheduledActivityInput;
    }): Promise<ScheduledActivity> => {
      const body: Record<string, unknown> = {};
      if (changes.type !== undefined) body.type = changes.type;
      if (changes.summary !== undefined) body.summary = changes.summary;
      if (changes.note !== undefined) body.note = changes.note;
      if (changes.dueDate !== undefined) body.due_date = changes.dueDate;
      if (changes.assigneeId !== undefined) body.assignee_id = changes.assigneeId;
      return toScheduledActivity(await client.patch<ScheduledActivityWire>(`${basePath}/${id}`, body));
    },
    onSuccess: () => refetchActivities(queryClient),
  };
}

// Completing writes an activity_done entry to the record's feed, so the
// feed refetches along with the activity lists.
export function createMarkActivityDoneMutationOptions(
  queryClient: QueryClient,
  client: Pick<APIClient, "post"> = apiClient,
) {
  return {
    mutationFn: async ({
      id,
      input,
    }: {
      id: string;
      input: MarkActivityDoneInput | undefined;
    }): Promise<ScheduledActivity> =>
      toScheduledActivity(
        await client.post<ScheduledActivityWire>(
          `${basePath}/${id}/done`,
          input?.feedback !== undefined ? { feedback: input.feedback } : {},
        ),
      ),
    onSuccess: (done: ScheduledActivity) =>
      Promise.all([
        refetchActivities(queryClient),
        queryClient.invalidateQueries({ queryKey: recordActivityQueryKey(done.model, done.recordId) }),
      ]),
  };
}

export function createCancelActivityMutationOptions(
  queryClient: QueryClient,
  client: Pick<APIClient, "delete"> = apiClient,
) {
  return {
    mutationFn: (id: string) => client.delete<void>(`${basePath}/${id}`),
    onSuccess: () => refetchActivities(queryClient),
  };
}

function useScheduledActivityChanges(): ScheduledActivityChanges {
  const queryClient = useQueryClient();
  const updateMutation = useMutation(createUpdateActivityMutationOptions(queryClient));
  const doneMutation = useMutation(createMarkActivityDoneMutationOptions(queryClient));
  const cancelMutation = useMutation(createCancelActivityMutationOptions(queryClient));
  const [pendingIds, setPendingIds] = useState<readonly string[]>([]);

  const track = async <T>(id: string, run: () => Promise<T>): Promise<T> => {
    setPendingIds((current) => [...current, id]);
    try {
      return await run();
    } finally {
      setPendingIds((current) => {
        const i = current.indexOf(id);
        return i < 0 ? current : [...current.slice(0, i), ...current.slice(i + 1)];
      });
    }
  };

  return {
    update: (id, changes) => track(id, () => updateMutation.mutateAsync({ id, changes })),
    markDone: (id, input) => track(id, () => doneMutation.mutateAsync({ id, input })),
    cancel: (id) => track(id, () => cancelMutation.mutateAsync(id)),
    pendingIds,
  };
}

export function useScheduledActivities(model: string, recordId: string): UseScheduledActivitiesResult {
  const queryClient = useQueryClient();
  const query = useQuery<ScheduledActivity[], AppError>(createScheduledActivitiesQueryOptions(model, recordId));
  const scheduleMutation = useMutation(createScheduleActivityMutationOptions(queryClient, model, recordId));
  const changes = useScheduledActivityChanges();

  return {
    activities: query.data ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    refetch: () => void query.refetch(),
    schedule: (input) => scheduleMutation.mutateAsync(input),
    isScheduling: scheduleMutation.isPending,
    ...changes,
  };
}

export function useMyActivities(options: UseMyActivitiesOptions = {}): UseMyActivitiesResult {
  const query = useInfiniteQuery<
    PagedResponse<MyScheduledActivity>,
    AppError,
    InfiniteData<PagedResponse<MyScheduledActivity>>,
    QueryKey,
    string | undefined
  >(createMyActivitiesQueryOptions(options));
  const changes = useScheduledActivityChanges();

  return {
    activities: query.data?.pages.flatMap((page) => page.data) ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    hasMore: query.hasNextPage,
    fetchMore: () => {
      void query.fetchNextPage();
    },
    isFetchingNextPage: query.isFetchingNextPage,
    refetch: () => void query.refetch(),
    ...changes,
  };
}
