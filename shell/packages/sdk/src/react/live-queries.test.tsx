import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resourceRef } from "../names.js";
import { wsManager } from "../realtime/ws-manager.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { useInfiniteList } from "./use-infinite-list.js";
import { useRecord } from "./use-record.js";

const { resolve, list, record } = vi.hoisted(() => ({
  resolve: vi.fn(async (resource: string) => ({ module: "crm", resource })),
  list: vi.fn(async (resource: string) => ({ data: [{ id: "1", name: resource }], meta: { hasMore: false } })),
  record: vi.fn(async () => ({ id: "1", name: "Saved" })),
}));

vi.mock("../schema/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../schema/index.js")>()),
  resourceRegistry: { resolve },
}));
vi.mock("../api/resource-api.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/resource-api.js")>()),
  listResource: list,
  getResource: record,
}));

class Socket {
  static instances: Socket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  sent: { type: string; channel: string }[] = [];

  constructor() {
    Socket.instances.push(this);
  }
  send(frame: string) {
    this.sent.push(JSON.parse(frame));
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1006 });
  }
}

function socket() {
  return Socket.instances.at(-1)!;
}

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

beforeEach(() => {
  vi.clearAllMocks();
  Socket.instances = [];
  vi.stubGlobal("WebSocket", Socket);
  wsManager.connect();
  socket().open();
});
afterEach(() => {
  cleanup();
  wsManager.disconnect();
  vi.unstubAllGlobals();
});

const contact = resourceRef<{ id: string; name: string }>("directory.person");

function change(resource: unknown = "directory.person", channel = "feed:crm") {
  act(() => simulateRealtimeMessage(channel, { type: "custom", event: "records.changed", payload: { resource } }));
}

describe("live list and record queries", () => {
  it("does not subscribe after unmounting while the resource registry resolves", async () => {
    let complete!: (entry: { module: string; resource: string }) => void;
    resolve.mockReturnValueOnce(
      new Promise((done) => {
        complete = done;
      }),
    );
    const { wrapper } = setup();
    const live = renderHook(() => useInfiniteList(contact), { wrapper });
    live.unmount();
    await act(async () => complete({ module: "crm", resource: "directory.person" }));
    expect(socket().sent).toEqual([]);
  });

  it("uses the registered module, filters resource and event, and refetches shared and embedded queries once", async () => {
    const { wrapper } = setup();
    const first = renderHook(() => useInfiniteList(contact), { wrapper });
    renderHook(() => useInfiniteList(contact), { wrapper });
    const embedded = renderHook(() => useInfiniteList(contact, { cacheKeyPrefix: "embedded:1" }), { wrapper });
    const other = renderHook(() => useInfiniteList(resourceRef("directory.company")), { wrapper });
    await waitFor(() =>
      expect(
        first.result.current.isSuccess && embedded.result.current.isSuccess && other.result.current.isSuccess,
      ).toBe(true),
    );
    expect(socket().sent).toEqual([{ type: "subscribe", channel: "feed:crm" }]);
    expect(list).toHaveBeenCalledTimes(3);

    change("directory.person", "feed:directory");
    change("directory.unknown");
    act(() =>
      simulateRealtimeMessage("feed:crm", { type: "record.updated", resource: "directory.person", recordId: "1" }),
    );
    change(7);
    expect(list).toHaveBeenCalledTimes(3);

    change();
    await waitFor(() => expect(list).toHaveBeenCalledTimes(5));
    expect(list.mock.calls.filter(([resource]) => resource === "directory.company")).toHaveLength(1);
  });

  it("unsubscribes only after the last list leaves and does not refetch unmounted lists", async () => {
    const { wrapper } = setup();
    const first = renderHook(() => useInfiniteList(contact), { wrapper });
    const second = renderHook(() => useInfiniteList(contact), { wrapper });
    await waitFor(() => expect(first.result.current.isSuccess && second.result.current.isSuccess).toBe(true));
    first.unmount();
    expect(socket().sent).toEqual([{ type: "subscribe", channel: "feed:crm" }]);
    second.unmount();
    expect(socket().sent.at(-1)).toEqual({ type: "unsubscribe", channel: "feed:crm" });
    change();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("refetches active tree children and marks cached list variants stale", async () => {
    const { client, wrapper } = setup();
    const children = vi.fn(async () => ["Child"]);
    const cachedKey = ["infinite-list", "old-filter", "directory.person"];
    client.setQueryData(cachedKey, { pages: [], pageParams: [] });
    const rendered = renderHook(
      () => {
        const root = useInfiniteList(contact);
        const tree = useQuery({ queryKey: ["tree-children", "directory.person", "parent_id", "1"], queryFn: children });
        return { root, tree };
      },
      { wrapper },
    );
    await waitFor(() =>
      expect(rendered.result.current.root.isSuccess && rendered.result.current.tree.isSuccess).toBe(true),
    );
    change();
    await waitFor(() => expect(children).toHaveBeenCalledTimes(2));
    expect(client.getQueryState(cachedKey)?.isInvalidated).toBe(true);
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("refetches live lists and records once after reconnect, preserves local edits and rejoins presence", async () => {
    const { client, wrapper } = setup();
    const live = renderHook(() => ({ list: useInfiniteList(contact), record: useRecord(contact, "1") }), { wrapper });
    renderHook(() => ({ list: useInfiniteList(contact), record: useRecord(contact, "1") }), { wrapper });
    const inactive = renderHook(() => useRecord(contact, "2"), { wrapper });
    const unrelated = vi.fn(async () => "unchanged");
    renderHook(() => useQuery({ queryKey: ["unrelated"], queryFn: unrelated }), { wrapper });
    await waitFor(() =>
      expect(
        live.result.current.list.isSuccess &&
          !live.result.current.record.isLoading &&
          !inactive.result.current.isLoading,
      ).toBe(true),
    );
    inactive.unmount();
    const invalidations = vi.spyOn(client, "invalidateQueries");
    act(() => live.result.current.record.setField("name", "Local draft"));
    const leave = wsManager.joinPresence("record:directory.person:1");
    record.mockResolvedValueOnce({ id: "1", name: "Remote" });

    act(() => {
      socket().close();
      wsManager.connect();
      socket().open();
    });
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(live.result.current.record.record?.name).toBe("Remote"));
    expect(record).toHaveBeenCalledTimes(3);
    expect(invalidations).toHaveBeenCalledTimes(1);
    expect(unrelated).toHaveBeenCalledTimes(1);
    expect(live.result.current.record.localRecord?.name).toBe("Local draft");
    expect(socket().sent).toEqual([
      { type: "subscribe", channel: "record:directory.person:1" },
      { type: "subscribe", channel: "feed:crm" },
      { type: "presence.join", channel: "record:directory.person:1" },
    ]);
    leave();
  });

  it("does not restore terminally unavailable records on reconnect", async () => {
    const { wrapper } = setup();
    const live = renderHook(() => useRecord(contact, "1"), { wrapper });
    await waitFor(() => expect(live.result.current.isLoading).toBe(false));
    act(() =>
      simulateRealtimeMessage("record:directory.person:1", {
        type: "record.unavailable",
        resource: "directory.person",
        recordId: "1",
      }),
    );
    await waitFor(() => expect(live.result.current.isUnavailable).toBe(true));
    act(() => {
      socket().close();
      wsManager.connect();
      socket().open();
    });
    expect(socket().sent).toEqual([]);
    expect(record).toHaveBeenCalledTimes(1);
    expect(live.result.current.record).toBeUndefined();
  });
});

describe("useInfiniteList keepPreviousData", () => {
  function renderSorted(keepPreviousData: boolean) {
    const { wrapper } = setup();
    return renderHook(({ sort }: { sort: "name" | "-name" }) => useInfiniteList(contact, { sort, keepPreviousData }), {
      wrapper,
      initialProps: { sort: "name" },
    });
  }

  it("keeps returning the previous sort's pages while the new sort loads", async () => {
    const rendered = renderSorted(true);
    await waitFor(() => expect(rendered.result.current.data?.pages[0]?.data).toHaveLength(1));

    list.mockReturnValueOnce(new Promise(() => {}));
    rendered.rerender({ sort: "-name" });

    expect(rendered.result.current.isLoading).toBe(false);
    expect(rendered.result.current.isPlaceholderData).toBe(true);
    expect(rendered.result.current.data?.pages[0]?.data).toHaveLength(1);
  });

  it("returns no data while a changed sort loads by default", async () => {
    const rendered = renderSorted(false);
    await waitFor(() => expect(rendered.result.current.data?.pages[0]?.data).toHaveLength(1));

    list.mockReturnValueOnce(new Promise(() => {}));
    rendered.rerender({ sort: "-name" });

    expect(rendered.result.current.isLoading).toBe(true);
    expect(rendered.result.current.data).toBeUndefined();
  });
});
