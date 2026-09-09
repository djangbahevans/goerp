import {
  type InfiniteData,
  type QueryClient,
  type QueryKey,
  type UseMutationResult,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";

// typescript-sdk-reference.md §8's canonical Notification interface.
export interface Notification {
  id: string;
  type: string;
  module: string;
  title: string;
  body: string | null;
  actionUrl: string | null;
  icon: string | null;
  readAt: string | null;
  createdAt: string;
}

export interface UseNotificationsOptions {
  limit?: number;
}

export interface UseNotificationsResult {
  notifications: Notification[];
  isLoading: boolean;
  hasMore: boolean;
  fetchMore: () => void;
  // Beyond §8's minimal illustrative shape — needed for the trailing
  // spinner row, distinct from the first-load isLoading state.
  isFetchingNextPage: boolean;
}

const NOTIFICATIONS_QUERY_KEY: QueryKey = ["notifications"];
const UNREAD_COUNT_QUERY_KEY: QueryKey = ["notifications", "unread-count"];

type FeedClient = Pick<APIClient, "get">;
type MutationClient = Pick<APIClient, "post" | "delete">;

// go-sdk-reference.md §13 "Built-in notification routes": GET /_notif/feed,
// paginated per typescript-sdk-reference.md §4's cursor convention.
export function createNotificationsInfiniteQueryOptions(
  options: UseNotificationsOptions = {},
  client: FeedClient = apiClient,
) {
  return {
    queryKey: [...NOTIFICATIONS_QUERY_KEY, options.limit ?? null] as QueryKey,
    queryFn: async ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<Notification>> =>
      client.get<PagedResponse<Notification>>("/_notif/feed", {
        params: {
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: PagedResponse<Notification>) =>
      lastPage.meta.hasMore ? (lastPage.meta.cursor ?? undefined) : undefined,
  };
}

export function useNotifications(options: UseNotificationsOptions = {}): UseNotificationsResult {
  const query = useInfiniteQuery<
    PagedResponse<Notification>,
    Error,
    InfiniteData<PagedResponse<Notification>>,
    QueryKey,
    string | undefined
  >(createNotificationsInfiniteQueryOptions(options));

  return {
    notifications: query.data?.pages.flatMap((page) => page.data) ?? [],
    isLoading: query.isLoading,
    hasMore: query.hasNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
    fetchMore: () => {
      void query.fetchNextPage();
    },
  };
}

// GET /_notif/count.
export function createUnreadCountQueryOptions(client: FeedClient = apiClient) {
  return {
    queryKey: UNREAD_COUNT_QUERY_KEY,
    queryFn: (): Promise<{ count: number }> => client.get<{ count: number }>("/_notif/count"),
  };
}

export function useUnreadCount(): { count: number } {
  const query = useQuery(createUnreadCountQueryOptions());
  return { count: query.data?.count ?? 0 };
}

// invalidateQueries matches by key prefix (its default, non-exact mode), so
// invalidating the ["notifications"] prefix alone already covers
// UNREAD_COUNT_QUERY_KEY (["notifications", "unread-count"]) too.
function invalidateFeed(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: NOTIFICATIONS_QUERY_KEY });
}

// POST /_notif/{id}/read.
export function createMarkReadMutationOptions(queryClient: QueryClient, client: MutationClient = apiClient) {
  return {
    mutationFn: (id: string) => client.post<void>(`/_notif/${id}/read`),
    onSuccess: () => invalidateFeed(queryClient),
  };
}

export function useMarkRead(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>(createMarkReadMutationOptions(queryClient));
}

// POST /_notif/read-all.
export function createMarkAllReadMutationOptions(queryClient: QueryClient, client: MutationClient = apiClient) {
  return {
    mutationFn: () => client.post<void>("/_notif/read-all"),
    onSuccess: () => invalidateFeed(queryClient),
  };
}

export function useMarkAllRead(): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>(createMarkAllReadMutationOptions(queryClient));
}

// DELETE /_notif/{id}.
export function createDismissNotificationMutationOptions(queryClient: QueryClient, client: MutationClient = apiClient) {
  return {
    mutationFn: (id: string) => client.delete<void>(`/_notif/${id}`),
    onSuccess: () => invalidateFeed(queryClient),
  };
}

export function useDismissNotification(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>(createDismissNotificationMutationOptions(queryClient));
}
