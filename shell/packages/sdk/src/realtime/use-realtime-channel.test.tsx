import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { realtime } from "./realtime-api.js";
import type { RealtimeMessage } from "./realtime-message.js";
import { useRealtimeChannel } from "./use-realtime-channel.js";
import { wsManager } from "./ws-manager.js";

// A real WebSocketManager over a fake socket, so subscribe/unsubscribe
// frames and envelope parsing run exactly as they do in the shell.
const sockets = vi.hoisted(() => {
  class FakeSocket {
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onclose: ((event: { code: number; reason: string }) => void) | null = null;
    readyState = 0;
    sent: { type: string; channel: string }[] = [];
    send(data: string) {
      this.sent.push(JSON.parse(data));
    }
    close() {
      this.readyState = 3;
    }
  }
  return { FakeSocket, current: null as InstanceType<typeof FakeSocket> | null };
});

vi.mock("./ws-manager.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./ws-manager.js")>();
  const manager = new actual.WebSocketManager(() => {
    sockets.current = new sockets.FakeSocket();
    return sockets.current as unknown as WebSocket;
  });
  return { ...actual, wsManager: manager };
});

function socket() {
  if (!sockets.current) throw new Error("not connected");
  return sockets.current;
}

// Stands in for @goerp/sdk/testing's simulateRealtimeMessage: the engine's
// {channel, type, payload} envelope arriving on the socket.
function simulateRealtimeMessage(channel: string, type: string, payload?: unknown) {
  act(() => socket().onmessage?.({ data: JSON.stringify({ channel, type, payload }) }));
}

function frames() {
  return socket().sent.map((frame) => `${frame.type} ${frame.channel}`);
}

beforeEach(() => {
  wsManager.connect();
  socket().readyState = 1;
  socket().onopen?.();
});

afterEach(() => {
  cleanup();
  wsManager.disconnect();
});

// typescript-sdk-reference.md §7's example.
function OrderList(): ReactNode {
  const qc = useQueryClient();
  useRealtimeChannel("feed:sales", (message: RealtimeMessage) => {
    if (message.type === "record.updated" && message.resource === "sales.order") {
      qc.invalidateQueries({ queryKey: ["sales", "orders"] });
      if (message.record) {
        qc.setQueryData(["sales", "orders", "detail", (message.record as { id: string }).id], message.record);
      }
    }
  });
  return null;
}

describe("useRealtimeChannel", () => {
  it("runs the §7 example against a record.updated message on feed:sales", () => {
    const qc = new QueryClient();
    qc.setQueryData(["sales", "orders", "list"], [{ id: "o1", total: 100 }]);
    render(
      <QueryClientProvider client={qc}>
        <OrderList />
      </QueryClientProvider>,
    );
    expect(frames()).toEqual(["subscribe feed:sales"]);

    const record = { id: "o1", total: 250 };
    simulateRealtimeMessage("feed:sales", "record.updated", { resource: "sales.order", record_id: "o1", record });

    expect(qc.getQueryState(["sales", "orders", "list"])?.isInvalidated).toBe(true);
    expect(qc.getQueryData(["sales", "orders", "detail", "o1"])).toEqual(record);
  });

  it("hands every message on its channel to the handler, and nothing from other channels", () => {
    const handler = vi.fn();
    function Probe() {
      useRealtimeChannel("feed:sales", handler);
      return null;
    }
    render(<Probe />);
    simulateRealtimeMessage("feed:sales", "record.created", { resource: "sales.order", record_id: "o2" });
    simulateRealtimeMessage("feed:contacts", "record.created", { resource: "contacts.contact", record_id: "c1" });
    simulateRealtimeMessage("feed:sales", "order.exported", { count: 3 });

    expect(handler.mock.calls.map(([message]) => message)).toEqual([
      { type: "record.created", event: "record.created", resource: "sales.order", recordId: "o2" },
      { type: "custom", event: "order.exported", payload: { count: 3 } },
    ]);
  });

  it("suppresses messages the filter rejects", () => {
    const handler = vi.fn();
    function Probe() {
      useRealtimeChannel("feed:sales", handler, { filter: (message) => message.resource === "sales.order" });
      return null;
    }
    render(<Probe />);
    simulateRealtimeMessage("feed:sales", "record.updated", { resource: "sales.quote", record_id: "q1" });
    simulateRealtimeMessage("feed:sales", "record.updated", { resource: "sales.order", record_id: "o1" });

    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler.mock.calls[0]?.[0]).toMatchObject({ recordId: "o1" });
  });

  it("subscribes to nothing while enabled is false, and subscribes once it turns true", () => {
    const handler = vi.fn();
    function Probe({ enabled }: { enabled: boolean }) {
      useRealtimeChannel("feed:sales", handler, { enabled });
      return null;
    }
    const { rerender } = render(<Probe enabled={false} />);
    expect(frames()).toEqual([]);
    simulateRealtimeMessage("feed:sales", "record.updated", {});
    expect(handler).not.toHaveBeenCalled();

    rerender(<Probe enabled />);
    expect(frames()).toEqual(["subscribe feed:sales"]);
    rerender(<Probe enabled={false} />);
    expect(frames()).toEqual(["subscribe feed:sales", "unsubscribe feed:sales"]);
  });

  it("unsubscribes on unmount", () => {
    const handler = vi.fn();
    function Probe() {
      useRealtimeChannel("feed:sales", handler);
      return null;
    }
    const { unmount } = render(<Probe />);
    unmount();

    expect(frames()).toEqual(["subscribe feed:sales", "unsubscribe feed:sales"]);
    simulateRealtimeMessage("feed:sales", "record.updated", {});
    expect(handler).not.toHaveBeenCalled();
  });

  it("moves to the new channel when channel changes", () => {
    const handler = vi.fn();
    function Probe({ channel }: { channel: string }) {
      useRealtimeChannel(channel, handler);
      return null;
    }
    const { rerender } = render(<Probe channel="feed:sales" />);
    rerender(<Probe channel="feed:contacts" />);

    expect(frames()).toEqual(["subscribe feed:sales", "unsubscribe feed:sales", "subscribe feed:contacts"]);
    simulateRealtimeMessage("feed:sales", "record.updated", {});
    simulateRealtimeMessage("feed:contacts", "record.updated", {});
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("uses the latest handler and filter without resubscribing", () => {
    const first = vi.fn();
    const second = vi.fn();
    function Probe({ handler, resource }: { handler: (message: RealtimeMessage) => void; resource: string }) {
      useRealtimeChannel("feed:sales", handler, { filter: (message) => message.resource === resource });
      return null;
    }
    const { rerender } = render(<Probe handler={first} resource="sales.order" />);
    rerender(<Probe handler={second} resource="sales.quote" />);
    simulateRealtimeMessage("feed:sales", "record.updated", { resource: "sales.order" });
    simulateRealtimeMessage("feed:sales", "record.updated", { resource: "sales.quote" });

    expect(frames()).toEqual(["subscribe feed:sales"]);
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
    expect(second.mock.calls[0]?.[0]).toMatchObject({ resource: "sales.quote" });
  });
});

describe("realtime.subscribe", () => {
  it("delivers filtered RealtimeMessages outside React until unsubscribed", () => {
    const handler = vi.fn();
    const unsubscribe = realtime.subscribe("notifications", handler, {
      filter: (message) => message.event === "notification.new",
    });
    simulateRealtimeMessage("notifications", "notification.read_all");
    simulateRealtimeMessage("notifications", "notification.new", { id: "n1" });
    unsubscribe();
    simulateRealtimeMessage("notifications", "notification.new", { id: "n2" });

    expect(handler.mock.calls.map(([message]) => message)).toEqual([
      { type: "custom", event: "notification.new", payload: { id: "n1" } },
    ]);
    expect(frames()).toEqual(["subscribe notifications", "unsubscribe notifications"]);
  });
});
