import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { RequestOptions } from "../http/types.js";
import { createRecordActivityQueryOptions, useRecordActivity } from "./use-record-activity.js";

afterEach(() => vi.restoreAllMocks());

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const author = { id: "u1", name: "Ama Owusu", avatar_url: null };

interface FakeEntry {
  id: string;
  kind: "comment";
  body?: string;
  deleted: boolean;
  author: typeof author;
  created_at: string;
}

// An in-memory /_meta/activity with the engine's paging: ids sort by
// creation, pages are newest first, and the cursor is the last id returned.
function fakeServer(initialCount: number) {
  let next = 0;
  const entries: FakeEntry[] = [];
  const add = (body: string) => {
    next += 1;
    const entry: FakeEntry = {
      id: `e${String(next).padStart(4, "0")}`,
      kind: "comment",
      body,
      deleted: false,
      author,
      created_at: "2026-09-25T10:00:00Z",
    };
    entries.push(entry);
    return entry;
  };
  for (let i = 0; i < initialCount; i++) add(`comment ${i + 1}`);

  const get = vi.spyOn(apiClient, "get").mockImplementation(async (_path: string, options?: RequestOptions) => {
    const params = options?.params ?? {};
    const limit = Number(params.limit ?? 20);
    const cursor = params.cursor as string | undefined;
    const newestFirst = [...entries].sort((a, b) => b.id.localeCompare(a.id));
    const after = cursor ? newestFirst.filter((e) => e.id < cursor) : newestFirst;
    const page = after.slice(0, limit);
    const hasMore = after.length > limit;
    return { data: page, meta: { cursor: hasMore ? page[page.length - 1]?.id : null, has_more: hasMore } } as never;
  });
  const post = vi.spyOn(apiClient, "post").mockImplementation(async (_path: string, body?: unknown) => {
    return add((body as { body: string }).body) as never;
  });
  const del = vi.spyOn(apiClient, "delete").mockImplementation(async (path: string) => {
    const entry = entries.find((e) => path.endsWith(`/${e.id}`));
    if (entry) {
      entry.deleted = true;
      delete entry.body;
    }
    return undefined as never;
  });
  return { get, post, del };
}

describe("createRecordActivityQueryOptions", () => {
  it("requests the record's feed and maps each entry kind to camelCase", async () => {
    const get = vi.fn(async () => ({
      data: [
        {
          id: "a4",
          kind: "activity_done",
          activity: { activity_id: "s1", type: "call", summary: "Confirm", due_date: "2026-09-25", feedback: null },
          author: { id: "u1", name: null, avatar_url: "https://img" },
          created_at: "2026-09-25T12:00:00Z",
        },
        {
          id: "a3",
          kind: "change",
          changes: [{ field: "state", old: "draft", new: "confirmed" }],
          author: null,
          created_at: "2026-09-25T11:00:00Z",
        },
        { id: "a2", kind: "comment", deleted: true, author, created_at: "2026-09-25T10:30:00Z" },
        { id: "a1", kind: "created", author, created_at: "2026-09-25T10:00:00Z" },
      ],
      meta: { cursor: "a1", has_more: true },
    }));
    const options = createRecordActivityQueryOptions("sales.order", "o1", { limit: 4 }, { get } as never);

    const page = await (options.queryFn as (ctx: { pageParam: string | undefined }) => Promise<unknown>)({
      pageParam: "a9",
    });

    expect(get).toHaveBeenCalledWith("/_meta/activity", {
      params: { model: "sales.order", record_id: "o1", limit: 4, cursor: "a9" },
    });
    expect(page).toEqual({
      data: [
        {
          id: "a4",
          kind: "activity_done",
          activity: { activityId: "s1", type: "call", summary: "Confirm", dueDate: "2026-09-25", feedback: null },
          author: { id: "u1", name: null, avatarUrl: "https://img" },
          createdAt: "2026-09-25T12:00:00Z",
        },
        {
          id: "a3",
          kind: "change",
          changes: [{ field: "state", old: "draft", new: "confirmed" }],
          author: null,
          createdAt: "2026-09-25T11:00:00Z",
        },
        {
          id: "a2",
          kind: "comment",
          body: null,
          deleted: true,
          author: { id: "u1", name: "Ama Owusu", avatarUrl: null },
          createdAt: "2026-09-25T10:30:00Z",
        },
        {
          id: "a1",
          kind: "created",
          author: { id: "u1", name: "Ama Owusu", avatarUrl: null },
          createdAt: "2026-09-25T10:00:00Z",
        },
      ],
      meta: { cursor: "a1", hasMore: true },
    });
  });
});

describe("useRecordActivity", () => {
  it("pages through the whole feed newest first with no duplicates or gaps", async () => {
    fakeServer(7);
    const { result } = renderHook(() => useRecordActivity("sales.order", "o1", { limit: 3 }), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.entries).toHaveLength(3));

    while (result.current.hasMore) {
      const loaded = result.current.entries.length;
      act(() => result.current.fetchMore());
      await waitFor(() => expect(result.current.entries.length).toBeGreaterThan(loaded));
    }

    expect(result.current.entries.map((e) => e.id)).toEqual([
      "e0007",
      "e0006",
      "e0005",
      "e0004",
      "e0003",
      "e0002",
      "e0001",
    ]);
  });

  it("posting a comment refetches the loaded pages without duplicating the entry it shifted", async () => {
    const { post } = fakeServer(4);
    const { result } = renderHook(() => useRecordActivity("sales.order", "o1", { limit: 2 }), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.entries).toHaveLength(2));
    act(() => result.current.fetchMore());
    await waitFor(() => expect(result.current.entries).toHaveLength(4));

    let posted: Awaited<ReturnType<typeof result.current.postComment>> | undefined;
    await act(async () => {
      posted = await result.current.postComment("Move delivery to Friday.");
    });

    expect(post).toHaveBeenCalledWith("/_meta/activity", {
      model: "sales.order",
      record_id: "o1",
      body: "Move delivery to Friday.",
    });
    expect(posted).toMatchObject({ id: "e0005", kind: "comment", body: "Move delivery to Friday.", deleted: false });
    await waitFor(() => expect(result.current.entries[0]?.id).toBe("e0005"));
    const ids = result.current.entries.map((e) => e.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids.slice(0, 4)).toEqual(["e0005", "e0004", "e0003", "e0002"]);
  });

  it("deleting a comment refetches the feed and tracks the delete in flight", async () => {
    const { del } = fakeServer(2);
    const { result } = renderHook(() => useRecordActivity("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.entries).toHaveLength(2));

    let resolveDelete: () => void = () => {};
    del.mockImplementationOnce(async (path: string) => {
      await new Promise<void>((resolve) => {
        resolveDelete = resolve;
      });
      const original = del.getMockImplementation();
      return original ? original(path) : (undefined as never);
    });

    let pending: Promise<void> = Promise.resolve();
    act(() => {
      pending = result.current.deleteComment("e0002");
    });
    await waitFor(() => expect(result.current.deletingIds).toEqual(["e0002"]));
    await act(async () => {
      resolveDelete();
      await pending;
    });

    expect(del).toHaveBeenCalledWith("/_meta/activity/e0002");
    expect(result.current.deletingIds).toEqual([]);
    await waitFor(() => expect(result.current.entries[0]).toMatchObject({ id: "e0002", deleted: true, body: null }));
  });

  it("surfaces a load failure as isError with the server's AppError", async () => {
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "get").mockRejectedValue(failure);
    const { result } = renderHook(() => useRecordActivity("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(failure);
  });

  it("rejects a failed delete with the AppError and leaves the feed as it was", async () => {
    const { get } = fakeServer(1);
    const failure = new AppError({ code: "not_author", message: "not yours", httpStatus: 403 });
    vi.spyOn(apiClient, "delete").mockRejectedValue(failure);
    const { result } = renderHook(() => useRecordActivity("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.entries).toHaveLength(1));

    await expect(act(() => result.current.deleteComment("e0001"))).rejects.toBe(failure);
    expect(result.current.deletingIds).toEqual([]);
    expect(get).toHaveBeenCalledTimes(1);
  });
});
