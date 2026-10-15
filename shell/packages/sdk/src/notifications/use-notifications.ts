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
import { type RealtimeEnvelope, wsManager } from "../realtime/ws-manager.js";

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

const NOTIFICATIONS_CHANNEL = "notifications";

type FeedData = InfiniteData<PagedResponse<Notification>>;

function cachedFeedNotifications(queryClient: QueryClient): Notification[] {
  return queryClient
    .getQueriesData<FeedData>({ queryKey: FEED_QUERY_KEY })
    .flatMap(([, data]) => data?.pages.flatMap((page) => page.data) ?? []);
}

function updateFeeds(
  queryClient: QueryClient,
  update: (pages: PagedResponse<Notification>[]) => PagedResponse<Notification>[],
): void {
  queryClient.setQueriesData<FeedData>(
    { queryKey: FEED_QUERY_KEY },
    (data) => data && { ...data, pages: update(data.pages) },
  );
}

function setUnreadCount(queryClient: QueryClient, next: (count: number) => number): void {
  queryClient.setQueryData<{ count: number }>(
    UNREAD_COUNT_QUERY_KEY,
    (data) => data && { count: Math.max(0, next(data.count)) },
  );
}

function markCachedRead(queryClient: QueryClient, id: string | undefined): void {
  const readAt = new Date().toISOString();
  updateFeeds(queryClient, (pages) =>
    pages.map((page) => ({
      ...page,
      data: page.data.map((n) => (n.readAt === null && (id === undefined || n.id === id) ? { ...n, readAt } : n)),
    })),
  );
}

function invalidateFeed(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: NOTIFICATIONS_QUERY_KEY });
}

// Marks cached feed rows read in place, then refreshes everything except the
// unread feeds. Those are only marked stale, so rows just read stay listed
// until the unread view is next opened.
function applyRead(queryClient: QueryClient, id: string | undefined): void {
  markCachedRead(queryClient, id);
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

function applyNew(queryClient: QueryClient, payload: unknown): void {
  const wire = payload as Omit<NotificationWire, "read_at"> | null | undefined;
  if (typeof wire?.id !== "string") return;
  const known = cachedFeedNotifications(queryClient).some((n) => n.id === wire.id);
  const notification = toNotification({ ...wire, read_at: null });
  updateFeeds(queryClient, (pages) => {
    const [first, ...rest] = pages;
    if (!first || pages.some((page) => page.data.some((n) => n.id === notification.id))) return pages;
    return [{ ...first, data: [notification, ...first.data] }, ...rest];
  });
  if (!known) setUnreadCount(queryClient, (count) => count + 1);
}

// notification.read also reports a dismissal, which the payload does not tell
// apart, so the cached row is marked read rather than removed. A notification
// on no cached page leaves its read state unknown, so the count is refetched.
function applyRemoteRead(queryClient: QueryClient, payload: unknown): void {
  const id = (payload as { id?: unknown } | null | undefined)?.id;
  if (typeof id !== "string") return;
  const cached = cachedFeedNotifications(queryClient).filter((n) => n.id === id);
  if (cached.length === 0) {
    void queryClient.invalidateQueries({ queryKey: UNREAD_COUNT_QUERY_KEY });
    return;
  }
  if (cached.every((n) => n.readAt !== null)) return;
  markCachedRead(queryClient, id);
  setUnreadCount(queryClient, (count) => count - 1);
}

function handleNotificationMessage(queryClient: QueryClient, message: RealtimeEnvelope): void {
  switch (message.type) {
    case "notification.new":
      applyNew(queryClient, message.payload);
      break;
    case "notification.read":
      applyRemoteRead(queryClient, message.payload);
      break;
    case "notification.read_all":
      markCachedRead(queryClient, undefined);
      setUnreadCount(queryClient, () => 0);
      break;
  }
}

// Pushes sent while the socket is down are not replayed, so a reconnect
// refetches the feeds and the unread count.
export function subscribeNotifications(queryClient: QueryClient): () => void {
  const unsubscribe = wsManager.subscribe(NOTIFICATIONS_CHANNEL, (message) =>
    handleNotificationMessage(queryClient, message),
  );
  const unsubscribeReconnect = wsManager.subscribeReconnect(() => invalidateFeed(queryClient));
  return () => {
    unsubscribe();
    unsubscribeReconnect();
  };
}

export function useDismissNotification(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>(createDismissNotificationMutationOptions(queryClient));
}
