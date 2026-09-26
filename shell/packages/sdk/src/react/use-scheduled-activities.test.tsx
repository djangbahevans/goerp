import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { RequestOptions } from "../http/types.js";
import { useRecordActivity } from "./use-record-activity.js";
import {
  createMyActivitiesQueryOptions,
  createScheduledActivitiesQueryOptions,
  useMyActivities,
  useScheduledActivities,
} from "./use-scheduled-activities.js";

afterEach(() => vi.restoreAllMocks());

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const me = { id: "u1", name: "Ama Owusu", avatar_url: null };

interface FakeActivity {
  id: string;
  model: string;
  record_id: string;
  type: string;
  summary: string;
  note: string | null;
  due_date: string;
  assignee: typeof me;
  created_by: typeof me;
  created_at: string;
  done_at: string | null;
  done_by: typeof me | null;
  feedback: string | null;
}

// An in-memory /_meta/scheduled-activities (and the record feed it writes
// activity_done entries to) with the engine's ordering: open activities
// soonest due first, ties by id, and "mine" paged by a (due_date, id)
// cursor.
function fakeServer(dueDates: string[]) {
  let next = 0;
  const activities: FakeActivity[] = [];
  const feed: { id: string; kind: "activity_done"; activity: unknown; author: typeof me; created_at: string }[] = [];
  const add = (fields: Partial<FakeActivity>) => {
    next += 1;
    const activity: FakeActivity = {
      id: `s${String(next).padStart(4, "0")}`,
      model: "sales.order",
      record_id: "o1",
      type: "call",
      summary: `activity ${next}`,
      note: null,
      due_date: "2026-09-25",
      assignee: me,
      created_by: me,
      created_at: "2026-09-24T10:00:00Z",
      done_at: null,
      done_by: null,
      feedback: null,
      ...fields,
    };
    activities.push(activity);
    return activity;
  };
  for (const due_date of dueDates) add({ due_date });

  const open = () =>
    activities
      .filter((a) => a.done_at === null)
      .sort((a, b) => a.due_date.localeCompare(b.due_date) || a.id.localeCompare(b.id));
  const byPath = (path: string) => activities.find((a) => path.includes(`/${a.id}`));

  const get = vi.spyOn(apiClient, "get").mockImplementation(async (path: string, options?: RequestOptions) => {
    const params = options?.params ?? {};
    if (path === "/_meta/activity") {
      return { data: [...feed].reverse(), meta: { cursor: null, has_more: false } } as never;
    }
    if (path.endsWith("/mine")) {
      const limit = Number(params.limit ?? 50);
      const cursor = params.cursor as string | undefined;
      const all = open().map((a) => ({ ...a, record_name: "SO-0001" }));
      const after = cursor ? all.filter((a) => `${a.due_date},${a.id}` > cursor) : all;
      const page = after.slice(0, limit);
      const hasMore = after.length > limit;
      const last = page[page.length - 1];
      return {
        data: page,
        meta: { cursor: hasMore && last ? `${last.due_date},${last.id}` : null, has_more: hasMore },
      } as never;
    }
    return { data: open().filter((a) => a.model === params.model && a.record_id === params.record_id) } as never;
  });
  const post = vi.spyOn(apiClient, "post").mockImplementation(async (path: string, body?: unknown) => {
    if (path.endsWith("/done")) {
      const activity = byPath(path);
      if (!activity) throw new Error("unknown activity");
      activity.done_at = "2026-09-25T09:00:00Z";
      activity.done_by = me;
      activity.feedback = (body as { feedback?: string }).feedback ?? null;
      feed.push({
        id: `f${feed.length + 1}`,
        kind: "activity_done",
        activity: {},
        author: me,
        created_at: activity.done_at,
      });
      return { ...activity } as never;
    }
    const { model, record_id, type, summary, due_date } = body as Pick<
      FakeActivity,
      "model" | "record_id" | "type" | "summary" | "due_date"
    >;
    return add({ model, record_id, type, summary, due_date }) as never;
  });
  const patch = vi.spyOn(apiClient, "patch").mockImplementation(async (path: string, body?: unknown) => {
    const activity = byPath(path);
    if (!activity) throw new Error("unknown activity");
    const b = body as Record<string, string | null>;
    if (b.summary !== undefined) activity.summary = b.summary as string;
    if (b.due_date !== undefined) activity.due_date = b.due_date as string;
    if ("note" in b) activity.note = b.note ?? null;
    return { ...activity } as never;
  });
  const del = vi.spyOn(apiClient, "delete").mockImplementation(async (path: string) => {
    const activity = byPath(path);
    if (activity) activities.splice(activities.indexOf(activity), 1);
    return undefined as never;
  });
  return { get, post, patch, del };
}

// Every list a change can affect, on one QueryClient.
function useAllLists() {
  return {
    record: useScheduledActivities("sales.order", "o1"),
    mine: useMyActivities({ limit: 2 }),
    feed: useRecordActivity("sales.order", "o1"),
  };
}

describe("query options", () => {
  it("requests a record's open activities and maps them to camelCase", async () => {
    const get = vi.fn(async () => ({
      data: [
        {
          id: "s1",
          model: "sales.order",
          record_id: "o1",
          type: "call",
          summary: "Confirm Friday delivery",
          note: "Morning slot",
          due_date: "2026-09-25",
          assignee: { id: "u2", name: null, avatar_url: "https://img" },
          created_by: me,
          created_at: "2026-09-23T16:02:00Z",
          done_at: null,
          done_by: null,
          feedback: null,
        },
      ],
    }));
    const options = createScheduledActivitiesQueryOptions("sales.order", "o1", { get } as never);

    const activities = await options.queryFn();

    expect(get).toHaveBeenCalledWith("/_meta/scheduled-activities", {
      params: { model: "sales.order", record_id: "o1" },
    });
    expect(activities).toEqual([
      {
        id: "s1",
        model: "sales.order",
        recordId: "o1",
        type: "call",
        summary: "Confirm Friday delivery",
        note: "Morning slot",
        dueDate: "2026-09-25",
        assignee: { id: "u2", name: null, avatarUrl: "https://img" },
        createdBy: { id: "u1", name: "Ama Owusu", avatarUrl: null },
        createdAt: "2026-09-23T16:02:00Z",
        doneAt: null,
        doneBy: null,
        feedback: null,
      },
    ]);
  });

  it("requests a page of the caller's activities with its cursor and keeps record_name", async () => {
    const get = vi.fn(async () => ({
      data: [
        {
          id: "s1",
          model: "sales.order",
          record_id: "o1",
          type: "todo",
          summary: "x",
          note: null,
          due_date: "2026-09-25",
          assignee: me,
          created_by: me,
          created_at: "2026-09-23T16:02:00Z",
          done_at: null,
          done_by: null,
          feedback: null,
          record_name: "SO-0001",
        },
      ],
      meta: { cursor: "abc", has_more: true },
    }));
    const options = createMyActivitiesQueryOptions({ limit: 10 }, { get } as never);

    const page = await options.queryFn({ pageParam: "prev" });

    expect(get).toHaveBeenCalledWith("/_meta/scheduled-activities/mine", { params: { limit: 10, cursor: "prev" } });
    expect(page.data[0]).toMatchObject({ id: "s1", recordId: "o1", recordName: "SO-0001" });
    expect(page.meta).toEqual({ cursor: "abc", hasMore: true });
  });
});

describe("useMyActivities", () => {
  it("pages through every open activity soonest first with no duplicates or gaps", async () => {
    fakeServer(["2026-09-27", "2026-09-25", "2026-09-25", "2026-09-26", "2026-09-24"]);
    const { result } = renderHook(() => useMyActivities({ limit: 2 }), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.activities).toHaveLength(2));

    while (result.current.hasMore) {
      const loaded = result.current.activities.length;
      act(() => result.current.fetchMore());
      await waitFor(() => expect(result.current.activities.length).toBeGreaterThan(loaded));
    }

    expect(result.current.activities.map((a) => a.id)).toEqual(["s0005", "s0002", "s0003", "s0004", "s0001"]);
  });
});

describe("mutations", () => {
  it("scheduling refreshes the record's list and My activities", async () => {
    const { post } = fakeServer(["2026-09-26"]);
    const { result } = renderHook(useAllLists, { wrapper: wrapper() });
    await waitFor(() => expect(result.current.mine.activities).toHaveLength(1));

    let scheduled: Awaited<ReturnType<typeof result.current.record.schedule>> | undefined;
    await act(async () => {
      scheduled = await result.current.record.schedule({
        type: "meeting",
        summary: "Kick-off",
        dueDate: "2026-09-25",
        assigneeId: "u1",
      });
    });

    expect(post).toHaveBeenCalledWith("/_meta/scheduled-activities", {
      model: "sales.order",
      record_id: "o1",
      type: "meeting",
      summary: "Kick-off",
      due_date: "2026-09-25",
      assignee_id: "u1",
    });
    expect(scheduled).toMatchObject({ id: "s0002", type: "meeting", dueDate: "2026-09-25" });
    await waitFor(() => expect(result.current.record.activities.map((a) => a.id)).toEqual(["s0002", "s0001"]));
    await waitFor(() => expect(result.current.mine.activities.map((a) => a.id)).toEqual(["s0002", "s0001"]));
  });

  it("marking done refreshes both lists and the record's feed, and tracks the activity in flight", async () => {
    const { post } = fakeServer(["2026-09-25", "2026-09-26"]);
    const { result } = renderHook(useAllLists, { wrapper: wrapper() });
    await waitFor(() => expect(result.current.mine.activities).toHaveLength(2));
    await waitFor(() => expect(result.current.feed.entries).toHaveLength(0));

    let release: () => void = () => {};
    const original = post.getMockImplementation();
    post.mockImplementationOnce(async (path: string, body?: unknown) => {
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return original ? original(path, body) : (undefined as never);
    });

    let pending: Promise<unknown> = Promise.resolve();
    act(() => {
      pending = result.current.mine.markDone("s0001", { feedback: "Confirmed." });
    });
    await waitFor(() => expect(result.current.mine.pendingIds).toEqual(["s0001"]));
    await act(async () => {
      release();
      await pending;
    });

    expect(post).toHaveBeenCalledWith("/_meta/scheduled-activities/s0001/done", { feedback: "Confirmed." });
    expect(result.current.mine.pendingIds).toEqual([]);
    await waitFor(() => expect(result.current.record.activities.map((a) => a.id)).toEqual(["s0002"]));
    await waitFor(() => expect(result.current.mine.activities.map((a) => a.id)).toEqual(["s0002"]));
    await waitFor(() => expect(result.current.feed.entries).toHaveLength(1));
  });

  it("marking done without feedback sends an empty body", async () => {
    const { post } = fakeServer(["2026-09-25"]);
    const { result } = renderHook(() => useScheduledActivities("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.activities).toHaveLength(1));

    await act(async () => {
      await result.current.markDone("s0001");
    });

    expect(post).toHaveBeenCalledWith("/_meta/scheduled-activities/s0001/done", {});
  });

  it("editing sends only the changed fields, clears a note with null, and re-sorts both lists", async () => {
    const { patch } = fakeServer(["2026-09-25", "2026-09-26"]);
    const { result } = renderHook(useAllLists, { wrapper: wrapper() });
    await waitFor(() => expect(result.current.mine.activities).toHaveLength(2));

    await act(async () => {
      await result.current.record.update("s0001", { dueDate: "2026-09-30", note: null });
    });

    expect(patch).toHaveBeenCalledWith("/_meta/scheduled-activities/s0001", { due_date: "2026-09-30", note: null });
    await waitFor(() => expect(result.current.record.activities.map((a) => a.id)).toEqual(["s0002", "s0001"]));
    await waitFor(() => expect(result.current.mine.activities.map((a) => a.id)).toEqual(["s0002", "s0001"]));
  });

  it("cancelling removes the activity from both lists without touching the feed", async () => {
    const { del, get } = fakeServer(["2026-09-25", "2026-09-26"]);
    const { result } = renderHook(useAllLists, { wrapper: wrapper() });
    await waitFor(() => expect(result.current.mine.activities).toHaveLength(2));
    const feedLoads = () => get.mock.calls.filter(([path]) => path === "/_meta/activity").length;
    const before = feedLoads();

    await act(async () => {
      await result.current.record.cancel("s0001");
    });

    expect(del).toHaveBeenCalledWith("/_meta/scheduled-activities/s0001");
    await waitFor(() => expect(result.current.record.activities.map((a) => a.id)).toEqual(["s0002"]));
    await waitFor(() => expect(result.current.mine.activities.map((a) => a.id)).toEqual(["s0002"]));
    expect(feedLoads()).toBe(before);
  });

  it("rejects a failed change with the server's AppError and refetches nothing", async () => {
    const { get } = fakeServer(["2026-09-25"]);
    const failure = new AppError({ code: "activity_done", message: "already done", httpStatus: 409 });
    vi.spyOn(apiClient, "patch").mockRejectedValue(failure);
    const { result } = renderHook(() => useScheduledActivities("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.activities).toHaveLength(1));

    await expect(act(() => result.current.update("s0001", { summary: "x" }))).rejects.toBe(failure);
    expect(result.current.pendingIds).toEqual([]);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("surfaces a load failure as isError with the server's AppError", async () => {
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "get").mockRejectedValue(failure);
    const { result } = renderHook(() => useScheduledActivities("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(failure);
  });
});
