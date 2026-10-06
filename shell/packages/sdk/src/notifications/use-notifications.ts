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
import { type PagedResponseWire, toPagedResponse } from "../http/paged-response.js";
import type { APIClient, PagedResponse } from "../http/types.js";

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
  unread?: boolean;
}

export interface UseNotificationsResult {
  notifications: Notification[];
  isLoading: boolean;
  hasMore: boolean;
  fetchMore: () => void;
  isFetchingNextPage: boolean;
}

interface NotificationWire {
  id: string;
  type: string;
  module: string;
  title: string;
  body: string | null;
  action_url: string | null;
  icon: string | null;
  read_at: string | null;
  created_at: string;
}

function toNotification(wire: NotificationWire): Notification {
  return {
    id: wire.id,
    type: wire.type,
    module: wire.module,
    title: wire.title,
    body: wire.body,
    actionUrl: wire.action_url,
    icon: wire.icon,
    readAt: wire.read_at,
    createdAt: wire.created_at,
  };
}

const NOTIFICATIONS_QUERY_KEY: QueryKey = ["notifications"];
const FEED_QUERY_KEY: QueryKey = [...NOTIFICATIONS_QUERY_KEY, "feed"];
const UNREAD_FEED_QUERY_KEY: QueryKey = [...FEED_QUERY_KEY, "unread"];
const UNREAD_COUNT_QUERY_KEY: QueryKey = ["notifications", "unread-count"];

type FeedClient = Pick<APIClient, "get">;
type MutationClient = Pick<APIClient, "post" | "delete">;

export function createNotificationsInfiniteQueryOptions(
  options: UseNotificationsOptions = {},
  client: FeedClient = apiClient,
) {
  return {
    queryKey: [...(options.unread ? UNREAD_FEED_QUERY_KEY : FEED_QUERY_KEY), options.limit ?? null] as QueryKey,
    queryFn: async ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<Notification>> => {
      const wire = await client.get<PagedResponseWire<NotificationWire>>("/_notif/feed", {
        params: {
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(options.unread ? { unread: true } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
      });
      return toPagedResponse(wire, toNotification);
    },
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

export function createUnreadCountQueryOptions(client: FeedClient = apiClient) {
  return {
    queryKey: UNREAD_COUNT_QUERY_KEY,
    queryFn: (): Promise<{ count: number }> => client.get<{ count: number }>("/_notif/count", { background: true }),
  };
}

export function useUnreadCount(): { count: number } {
  const query = useQuery(createUnreadCountQueryOptions());
  return { count: query.data?.count ?? 0 };
}

function invalidateFeed(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: NOTIFICATIONS_QUERY_KEY });
}

// Marks cached feed rows read in place, then refreshes everything except the
// unread feeds. Those are only marked stale, so rows just read stay listed
// until the unread view is next opened.
function applyRead(queryClient: QueryClient, id: string | undefined): void {
  const readAt = new Date().toISOString();
  queryClient.setQueriesData<InfiniteData<PagedResponse<Notification>>>(
    { queryKey: FEED_QUERY_KEY },
    (data) =>
      data && {
        ...data,
        pages: data.pages.map((page) => ({
          ...page,
          data: page.data.map((n) => (n.readAt === null && (id === undefined || n.id === id) ? { ...n, readAt } : n)),
        })),
      },
  );
  void queryClient.invalidateQueries({
    queryKey: NOTIFICATIONS_QUERY_KEY,
    predicate: (query) => !UNREAD_FEED_QUERY_KEY.every((part, i) => query.queryKey[i] === part),
  });
  void queryClient.invalidateQueries({ queryKey: UNREAD_FEED_QUERY_KEY, refetchType: "none" });
}

export function createMarkReadMutationOptions(queryClient: QueryClient, client: MutationClient = apiClient) {
  return {
    mutationFn: (id: string) => client.post<void>(`/_notif/${id}/read`),
    onSuccess: (_data: unknown, id: string) => applyRead(queryClient, id),
  };
}

export function useMarkRead(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>(createMarkReadMutationOptions(queryClient));
}

export function createMarkAllReadMutationOptions(queryClient: QueryClient, client: MutationClient = apiClient) {
  return {
    mutationFn: () => client.post<void>("/_notif/read-all"),
    onSuccess: () => applyRead(queryClient, undefined),
  };
}

export function useMarkAllRead(): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>(createMarkAllReadMutationOptions(queryClient));
}

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
