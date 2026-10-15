import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PagedResponseWire } from "../http/paged-response.js";
import { resourceRef } from "../names.js";
import { wsManager } from "../realtime/ws-manager.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { type ListOptions, useList } from "./use-list.js";

type Person = { id: string; name: string; created_at: string };

const { resolve, get } = vi.hoisted(() => ({
  resolve: vi.fn(async (resource: string) => ({ module: "crm", resource, listPath: "/crm/people" })),
  get: vi.fn(),
}));

vi.mock("../schema/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../schema/index.js")>()),
  resourceRegistry: { resolve },
}));
vi.mock("../http/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../http/index.js")>()),
  apiClient: { get },
}));

const pages: Record<string, PagedResponseWire<Person>> = {
  first: { data: [{ id: "1", name: "Ada", created_at: "" }], meta: { cursor: "c2", has_more: true } },
  c2: { data: [{ id: "2", name: "Grace", created_at: "" }], meta: { cursor: "c3", has_more: true } },
  c3: { data: [{ id: "3", name: "Edsger", created_at: "" }], meta: { cursor: null, has_more: false } },
};

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

const person = resourceRef<Person>("crm.person");

function render(options: ListOptions<typeof person> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook((props: ListOptions<typeof person>) => useList(person, props), {
    wrapper,
    initialProps: options,
  });
}

function changed(channel: string, resource = "crm.person") {
  act(() => simulateRealtimeMessage(channel, { type: "custom", event: "records.changed", payload: { resource } }));
}

beforeEach(() => {
  vi.clearAllMocks();
  get.mockImplementation(async (_path: string, { params }: { params: { cursor?: string } }) => ({
    ...pages[params.cursor ?? "first"],
  }));
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

describe("useList", () => {
  it("sends filter, sort and the default limit to the resource's list route", async () => {
    const { result } = render({ filter: { name: "Ada" }, sort: ["-created_at", "name"] });
    await waitFor(() => expect(result.current.data?.data).toEqual(pages.first!.data));
    expect(resolve).toHaveBeenCalledWith("crm.person");
    expect(get).toHaveBeenCalledWith("/crm/people", {
      params: { "filter[name]": "Ada", sort: "-created_at,name", limit: 50 },
    });
    expect(result.current.cursor).toBeUndefined();
    expect(result.current.hasMore).toBe(true);
  });

  it("starts at the given cursor with the given limit", async () => {
    const { result } = render({ cursor: "c2", limit: 10 });
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Grace"));
    expect(get).toHaveBeenCalledWith("/crm/people", { params: { limit: 10, cursor: "c2" } });
    expect(result.current.cursor).toBe("c2");
  });

  it("advances by cursor and returns to the previous cursor", async () => {
    const { result } = render();
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Ada"));

    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Grace"));
    expect(result.current.cursor).toBe("c2");
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Edsger"));
    expect(result.current.hasMore).toBe(false);
    act(() => result.current.nextPage());
    expect(result.current.cursor).toBe("c3");

    act(() => result.current.prevPage());
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Grace"));
    act(() => result.current.prevPage());
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Ada"));
    expect(result.current.cursor).toBeUndefined();
    act(() => result.current.prevPage());
    expect(result.current.cursor).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("returns to the first page when the query changes", async () => {
    const { result, rerender } = render({ sort: "name" });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.cursor).toBe("c2"));

    rerender({ sort: "-name" });
    expect(result.current.cursor).toBeUndefined();
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Ada"));
    expect(get).toHaveBeenLastCalledWith("/crm/people", { params: { sort: "-name", limit: 50 } });
  });

  it("does not restore a page when an earlier query returns", async () => {
    const { result, rerender } = render({ sort: "name" });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.cursor).toBe("c2"));

    rerender({ sort: "-name" });
    rerender({ sort: "name" });
    expect(result.current.cursor).toBeUndefined();
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("Ada"));
  });

  it("applies select and does not fetch while disabled", async () => {
    const { result, rerender } = render({ enabled: false });
    expect(result.current.isLoading).toBe(false);
    expect(get).not.toHaveBeenCalled();
    expect(socket().sent).toEqual([]);

    rerender({ select: (page) => ({ ...page, data: page.data.map((p) => ({ ...p, name: p.name.toUpperCase() })) }) });
    await waitFor(() => expect(result.current.data?.data[0]?.name).toBe("ADA"));
  });

  it("refetches on a records.changed message for its resource on the module feed", async () => {
    const { result } = render();
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(socket().sent).toEqual([{ type: "subscribe", channel: "feed:crm" }]);

    changed("feed:crm", "crm.company");
    changed("feed:other");
    expect(get).toHaveBeenCalledTimes(1);

    changed("feed:crm");
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("refetches on an overridden channel", async () => {
    const { result } = render({ realtimeChannel: "team:sales" });
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(socket().sent).toEqual([{ type: "subscribe", channel: "team:sales" }]);
    expect(resolve).toHaveBeenCalledTimes(1);

    changed("feed:crm");
    expect(get).toHaveBeenCalledTimes(1);
    changed("team:sales");
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("subscribes to nothing when realtimeChannel is null", async () => {
    const { result } = render({ realtimeChannel: null });
    await waitFor(() => expect(result.current.data).toBeDefined());
    changed("feed:crm");
    expect(socket().sent).toEqual([]);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("unsubscribes on unmount", async () => {
    const { result, unmount } = render();
    await waitFor(() => expect(socket().sent).toHaveLength(1));
    expect(result.current.data).toBeDefined();
    unmount();
    expect(socket().sent.at(-1)).toEqual({ type: "unsubscribe", channel: "feed:crm" });
  });

  it("exposes request errors", async () => {
    get.mockRejectedValueOnce(new Error("boom"));
    const { result } = render();
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.message).toBe("boom");
    expect(result.current.hasMore).toBe(false);
  });

  it("refetches the current page on demand", async () => {
    const { result } = render();
    await waitFor(() => expect(result.current.data).toBeDefined());
    act(() => result.current.refetch());
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });
});
