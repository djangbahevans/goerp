import { type InfiniteData, QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { Notification } from "./use-notifications.js";
import {
  createDismissNotificationMutationOptions,
  createMarkAllReadMutationOptions,
  createMarkReadMutationOptions,
  createNotificationsInfiniteQueryOptions,
  createUnreadCountQueryOptions,
} from "./use-notifications.js";

// notification-system.md §9 "Feed response shape".
const feedResponseExample = {
  data: [
    {
      id: "01j-notif",
      type: "sales.order_confirmed",
      module: "sales",
      title: "Order ORD-0042 confirmed",
      body: "Order for Acme Corp confirmed. Total: GH₵1,234.56",
      action_url: "/_m/sales/orders/01j-order",
      icon: "shopping-cart",
      read_at: null,
      created_at: "2026-05-16T10:22:31Z",
    },
  ],
  meta: { cursor: "01j-cursor", has_more: true, unread: 12 },
};

const emptyFeedResponse = { data: [], meta: { cursor: null, has_more: false, unread: 0 } };

function fakeGetClient(response: unknown): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

function fakeMutationClient(): Pick<APIClient, "post" | "delete"> {
  const post = vi.fn(async () => undefined);
  const del = vi.fn(async () => undefined);
  return { post, delete: del } as unknown as Pick<APIClient, "post" | "delete">;
}

async function callQueryFn<T>(
  options: { queryFn: unknown; queryKey: readonly unknown[] },
  pageParam: string | undefined,
): Promise<T> {
  const fn = options.queryFn as (ctx: {
    pageParam: string | undefined;
    queryKey: readonly unknown[];
    meta: undefined;
    direction: "forward";
  }) => Promise<T>;
  return fn({ pageParam, queryKey: options.queryKey, meta: undefined, direction: "forward" });
}

describe("createNotificationsInfiniteQueryOptions", () => {
  it("fetches /_notif/feed with limit and no cursor on the first page", async () => {
    const client = fakeGetClient(emptyFeedResponse);
    const options = createNotificationsInfiniteQueryOptions({ limit: 20 }, client);

    await callQueryFn(options, undefined);

    expect(client.get).toHaveBeenCalledWith("/_notif/feed", { params: { limit: 20 } });
  });

  it("sends unread=true only when the unread filter is on, under its own cache key", async () => {
    const client = fakeGetClient(emptyFeedResponse);
    const unread = createNotificationsInfiniteQueryOptions({ limit: 20, unread: true }, client);
    const all = createNotificationsInfiniteQueryOptions({ limit: 20, unread: false }, client);

    await callQueryFn(unread, "page-2");
    await callQueryFn(all, undefined);

    expect(client.get).toHaveBeenNthCalledWith(1, "/_notif/feed", {
      params: { limit: 20, unread: true, cursor: "page-2" },
    });
    expect(client.get).toHaveBeenNthCalledWith(2, "/_notif/feed", { params: { limit: 20 } });
    expect(unread.queryKey).not.toEqual(all.queryKey);
  });

  it("sends the page param as a cursor on subsequent pages", async () => {
    const client = fakeGetClient(emptyFeedResponse);
    const options = createNotificationsInfiniteQueryOptions({}, client);

    await callQueryFn(options, "page-2");

    expect(client.get).toHaveBeenCalledWith("/_notif/feed", { params: { cursor: "page-2" } });
  });

  it("maps the snake_case feed response to camelCase notifications and meta", async () => {
    const options = createNotificationsInfiniteQueryOptions({}, fakeGetClient(feedResponseExample));

    const page = await callQueryFn<PagedResponse<Notification>>(options, undefined);

    expect(page).toEqual({
      data: [
        {
          id: "01j-notif",
          type: "sales.order_confirmed",
          module: "sales",
          title: "Order ORD-0042 confirmed",
          body: "Order for Acme Corp confirmed. Total: GH₵1,234.56",
          actionUrl: "/_m/sales/orders/01j-order",
          icon: "shopping-cart",
          readAt: null,
          createdAt: "2026-05-16T10:22:31Z",
        },
      ],
      meta: { cursor: "01j-cursor", hasMore: true },
    });
  });

  it("requests the next page with the previous response's meta.cursor", async () => {
    const client = fakeGetClient(feedResponseExample);
    const options = createNotificationsInfiniteQueryOptions({}, client);

    const first = await callQueryFn<PagedResponse<Notification>>(options, undefined);
    const next = options.getNextPageParam(first);
    await callQueryFn(options, next);

    expect(next).toBe("01j-cursor");
    expect(client.get).toHaveBeenLastCalledWith("/_notif/feed", { params: { cursor: "01j-cursor" } });
  });

  it("advances to the next cursor only while hasMore is true", () => {
    const client = fakeGetClient(emptyFeedResponse);
    const options = createNotificationsInfiniteQueryOptions({}, client);

    expect(options.getNextPageParam({ data: [], meta: { cursor: "next", hasMore: true } })).toBe("next");
    expect(options.getNextPageParam({ data: [], meta: { cursor: "next", hasMore: false } })).toBe(undefined);
  });
});

describe("createUnreadCountQueryOptions", () => {
  it("fetches /_notif/count", async () => {
    const client = fakeGetClient({ count: 3 });
    const options = createUnreadCountQueryOptions(client);

    await expect(options.queryFn()).resolves.toEqual({ count: 3 });
    expect(client.get).toHaveBeenCalledWith("/_notif/count", { background: true });
  });
});

describe("notification mutations", () => {
  const unreadItem = (id: string): Notification => ({
    id,
    type: "t",
    module: "m",
    title: id,
    body: null,
    actionUrl: null,
    icon: null,
    readAt: null,
    createdAt: "2026-05-16T10:22:31Z",
  });

  function seededClient() {
    const queryClient = new QueryClient();
    const feed = (...ids: string[]): InfiniteData<PagedResponse<Notification>> => ({
      pages: [{ data: ids.map(unreadItem), meta: { cursor: null, hasMore: false } }],
      pageParams: [undefined],
    });
    const all = createNotificationsInfiniteQueryOptions({ limit: 20 }).queryKey;
    const unread = createNotificationsInfiniteQueryOptions({ limit: 20, unread: true }).queryKey;
    queryClient.setQueryData(all, feed("a", "b"));
    queryClient.setQueryData(unread, feed("a", "b"));
    queryClient.setQueryData(createUnreadCountQueryOptions().queryKey, { count: 2 });
    return { queryClient, all, unread };
  }

  const readIds = (queryClient: QueryClient, key: readonly unknown[]) =>
    queryClient
      .getQueryData<InfiniteData<PagedResponse<Notification>>>(key)
      ?.pages.flatMap((page) => page.data)
      .filter((n) => n.readAt !== null)
      .map((n) => n.id);

  it("createMarkReadMutationOptions POSTs /_notif/{id}/read and marks only that row read in every feed", async () => {
    const { queryClient, all, unread } = seededClient();
    const client = fakeMutationClient();
    const options = createMarkReadMutationOptions(queryClient, client);

    await options.mutationFn("a");
    options.onSuccess(undefined, "a");

    expect(client.post).toHaveBeenCalledWith("/_notif/a/read");
    expect(readIds(queryClient, all)).toEqual(["a"]);
    expect(readIds(queryClient, unread)).toEqual(["a"]);
  });

  it("invalidates the badge count and the all feed, leaving unread-feed rows in place", () => {
    const { queryClient, all, unread } = seededClient();
    createMarkAllReadMutationOptions(queryClient, fakeMutationClient()).onSuccess();

    const state = (key: readonly unknown[]) => queryClient.getQueryState(key)?.isInvalidated;
    expect(state(all)).toBe(true);
    expect(state(createUnreadCountQueryOptions().queryKey)).toBe(true);
    expect(state(unread)).toBe(true);
    expect(readIds(queryClient, unread)).toEqual(["a", "b"]);
  });

  it("createMarkAllReadMutationOptions POSTs /_notif/read-all and marks every cached row read", async () => {
    const { queryClient, all, unread } = seededClient();
    const client = fakeMutationClient();
    const options = createMarkAllReadMutationOptions(queryClient, client);

    await options.mutationFn();
    options.onSuccess();

    expect(client.post).toHaveBeenCalledWith("/_notif/read-all");
    expect(readIds(queryClient, all)).toEqual(["a", "b"]);
    expect(readIds(queryClient, unread)).toEqual(["a", "b"]);
  });

  it("createDismissNotificationMutationOptions DELETEs /_notif/{id}", async () => {
    const client = fakeMutationClient();
    const options = createDismissNotificationMutationOptions(new QueryClient(), client);

    await options.mutationFn("n1");

    expect(client.delete).toHaveBeenCalledWith("/_notif/n1");
  });
});
