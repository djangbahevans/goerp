import { QueryClient } from "@tanstack/react-query";
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
    expect(client.get).toHaveBeenCalledWith("/_notif/count");
  });
});

describe("notification mutations", () => {
  it("createMarkReadMutationOptions POSTs /_notif/{id}/read and invalidates the notifications prefix", async () => {
    const queryClient = new QueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const client = fakeMutationClient();
    const options = createMarkReadMutationOptions(queryClient, client);

    await options.mutationFn("n1");
    options.onSuccess();

    expect(client.post).toHaveBeenCalledWith("/_notif/n1/read");
    // A single prefix invalidation ("notifications") already covers the
    // unread-count query too, since invalidateQueries matches by key prefix.
    expect(invalidateSpy).toHaveBeenCalledExactlyOnceWith({ queryKey: ["notifications"] });
  });

  it("createMarkAllReadMutationOptions POSTs /_notif/read-all", async () => {
    const client = fakeMutationClient();
    const options = createMarkAllReadMutationOptions(new QueryClient(), client);

    await options.mutationFn();

    expect(client.post).toHaveBeenCalledWith("/_notif/read-all");
  });

  it("createDismissNotificationMutationOptions DELETEs /_notif/{id}", async () => {
    const client = fakeMutationClient();
    const options = createDismissNotificationMutationOptions(new QueryClient(), client);

    await options.mutationFn("n1");

    expect(client.delete).toHaveBeenCalledWith("/_notif/n1");
  });
});
