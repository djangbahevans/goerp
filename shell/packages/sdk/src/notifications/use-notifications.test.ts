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

function fakeNotification(overrides: Partial<Notification> = {}): Notification {
  return {
    id: "n1",
    type: "sales.order_confirmed",
    module: "sales",
    title: "Order confirmed",
    body: null,
    actionUrl: null,
    icon: null,
    readAt: null,
    createdAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

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
    const response: PagedResponse<Notification> = {
      data: [fakeNotification()],
      meta: { cursor: null, hasMore: false },
    };
    const client = fakeGetClient(response);
    const options = createNotificationsInfiniteQueryOptions({ limit: 20 }, client);

    await callQueryFn(options, undefined);

    expect(client.get).toHaveBeenCalledWith("/_notif/feed", { params: { limit: 20 } });
  });

  it("sends the page param as a cursor on subsequent pages", async () => {
    const response: PagedResponse<Notification> = { data: [], meta: { cursor: null, hasMore: false } };
    const client = fakeGetClient(response);
    const options = createNotificationsInfiniteQueryOptions({}, client);

    await callQueryFn(options, "page-2");

    expect(client.get).toHaveBeenCalledWith("/_notif/feed", { params: { cursor: "page-2" } });
  });

  it("advances to the next cursor only while hasMore is true", () => {
    const client = fakeGetClient({ data: [], meta: { cursor: null, hasMore: false } });
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
