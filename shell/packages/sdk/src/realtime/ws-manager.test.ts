import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthMachine } from "../auth/auth-machine.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import { tenantChannel, userChannel, WebSocketManager, wireWebSocketManager } from "./ws-manager.js";

const user: CurrentUser = {
  id: "u1",
  email: "a@example.com",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  firstDayOfWeek: "monday" as const,
  numberFormat: "1,234.56" as const,
  passwordMinLength: 12,
};

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 3;

class FakeSocket {
  static instances: FakeSocket[] = [];

  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  sent: string[] = [];
  closed = false;
  readyState = CONNECTING;

  constructor(public readonly url: string) {
    FakeSocket.instances.push(this);
  }

  // Match the browser's failure for sends before the handshake completes.
  send(data: string): void {
    if (this.readyState !== OPEN) {
      throw new Error("InvalidStateError: still in CONNECTING state");
    }
    this.sent.push(data);
  }

  close(code = 1000, reason = ""): void {
    if (this.closed) return;
    this.closed = true;
    this.readyState = CLOSED;
    this.onclose?.({ code, reason });
  }

  open(): void {
    this.readyState = OPEN;
    this.onopen?.();
  }

  message(envelope: unknown): void {
    this.onmessage?.({ data: JSON.stringify(envelope) });
  }

  serverClose(code: number, reason = ""): void {
    this.closed = true;
    this.readyState = CLOSED;
    this.onclose?.({ code, reason });
  }
}

function fakeFactory(): (url: string) => WebSocket {
  FakeSocket.instances = [];
  return (url: string) => new FakeSocket(url) as unknown as WebSocket;
}

function testManager(): WebSocketManager {
  return new WebSocketManager(fakeFactory(), () => "ws://localhost/_ws");
}

function latestSocket(): FakeSocket {
  const socket = FakeSocket.instances.at(-1);
  if (!socket) throw new Error("no FakeSocket constructed yet");
  return socket;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("tenantChannel", () => {
  it("formats the engine's tenant:{id} convention", () => {
    expect(tenantChannel("t1")).toBe("tenant:t1");
  });

  it("is distinct for distinct tenant ids", () => {
    expect(tenantChannel("t1")).not.toBe(tenantChannel("t2"));
  });
});

describe("userChannel", () => {
  it("formats the engine's ui:user:{id} convention", () => {
    expect(userChannel("u1")).toBe("ui:user:u1");
  });

  it("is distinct for distinct user ids", () => {
    expect(userChannel("u1")).not.toBe(userChannel("u2"));
  });
});

describe("WebSocketManager", () => {
  it("keeps reconnect registrations independent when they share a handler", () => {
    const manager = testManager();
    const recover = vi.fn();
    const stop = manager.subscribeReconnect(recover);
    manager.subscribeReconnect(recover);
    manager.connect();
    latestSocket().open();
    stop();
    stop();
    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1000);
    latestSocket().open();
    expect(recover).toHaveBeenCalledTimes(1);
  });

  it("reports only live channels once per reconnect and skips the first open", () => {
    const manager = testManager();
    const recover = vi.fn();
    const stopRecovering = manager.subscribeReconnect(recover);
    manager.connect();
    manager.subscribe("feed:sales", () => {});
    const unsubscribe = manager.subscribe("record:sales.order:o1", () => {});
    latestSocket().open();
    expect(recover).not.toHaveBeenCalled();

    latestSocket().serverClose(1006);
    unsubscribe();
    vi.advanceTimersByTime(1000);
    latestSocket().open();
    expect(recover).toHaveBeenCalledExactlyOnceWith(new Set(["feed:sales"]));

    stopRecovering();
    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1000);
    latestSocket().open();
    expect(recover).toHaveBeenCalledTimes(1);
  });

  it("replays shared presence once, retains it until the last member leaves, and drops departed joins", () => {
    const manager = testManager();
    manager.connect();
    const leaveA = manager.joinPresence("record:sales.order:o1");
    const leaveB = manager.joinPresence("record:sales.order:o1");
    const leaveOther = manager.joinPresence("record:sales.order:o2");
    latestSocket().open();
    expect(latestSocket().sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "subscribe", channel: "record:sales.order:o1" },
      { type: "subscribe", channel: "record:sales.order:o2" },
      { type: "presence.join", channel: "record:sales.order:o1" },
      { type: "presence.join", channel: "record:sales.order:o2" },
    ]);

    latestSocket().serverClose(1006);
    leaveA();
    leaveOther();
    vi.advanceTimersByTime(1000);
    latestSocket().open();
    expect(latestSocket().sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "subscribe", channel: "record:sales.order:o1" },
      { type: "presence.join", channel: "record:sales.order:o1" },
    ]);

    leaveB();
    leaveB();
    expect(
      latestSocket()
        .sent.slice(2)
        .map((frame) => JSON.parse(frame)),
    ).toEqual([
      { type: "presence.leave", channel: "record:sales.order:o1" },
      { type: "unsubscribe", channel: "record:sales.order:o1" },
    ]);
  });

  it("joins and leaves presence on an open socket while retaining other channel subscribers", () => {
    const manager = testManager();
    manager.connect();
    latestSocket().open();
    manager.subscribe("record:sales.order:o1", () => {});
    const leave = manager.joinPresence("record:sales.order:o1");
    leave();
    expect(latestSocket().sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "subscribe", channel: "record:sales.order:o1" },
      { type: "presence.join", channel: "record:sales.order:o1" },
      { type: "presence.leave", channel: "record:sales.order:o1" },
    ]);
  });

  it("ignores open and message events from a superseded socket", () => {
    const manager = testManager();
    const recover = vi.fn();
    const received = vi.fn();
    manager.subscribeReconnect(recover);
    manager.subscribe("feed:sales", received);
    manager.connect();
    const stale = latestSocket();
    manager.connect();
    stale.open();
    stale.message({ channel: "feed:sales", type: "records.changed", payload: { resource: "sales.order" } });
    expect(stale.sent).toEqual([]);
    expect(received).not.toHaveBeenCalled();
    latestSocket().open();
    expect(recover).not.toHaveBeenCalled();
  });

  it("resets reconnect recovery and presence across logout", () => {
    const manager = testManager();
    const recover = vi.fn();
    manager.subscribeReconnect(recover);
    manager.connect();
    const staleLeave = manager.joinPresence("record:sales.order:o1");
    latestSocket().open();
    manager.disconnect();
    manager.connect();
    manager.joinPresence("record:sales.order:o1");
    latestSocket().open();
    staleLeave();
    expect(recover).not.toHaveBeenCalled();
    expect(latestSocket().sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "subscribe", channel: "record:sales.order:o1" },
      { type: "presence.join", channel: "record:sales.order:o1" },
    ]);
  });

  it("reuses a single connection across multiple channel subscribers", () => {
    const manager = testManager();
    manager.connect();
    expect(FakeSocket.instances).toHaveLength(1);

    manager.subscribe("notifications", () => {});
    manager.subscribe("feed:sales", () => {});
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("sends a subscribe message only for the first subscriber to a channel", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();
    socket.open();

    manager.subscribe("notifications", () => {});
    manager.subscribe("notifications", () => {});

    const subscribeMessages = socket.sent.filter((s) => JSON.parse(s).type === "subscribe");
    expect(subscribeMessages).toHaveLength(1);
  });

  it("delivers a message to every subscriber of its channel", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();

    const received: unknown[] = [];
    manager.subscribe("notifications", (msg) => received.push(msg));
    manager.subscribe("notifications", (msg) => received.push(msg));
    manager.subscribe("feed:sales", (msg) => received.push(msg));

    socket.message({ channel: "notifications", type: "notification.new", payload: { id: "n1" } });

    expect(received).toEqual([
      { channel: "notifications", type: "notification.new", payload: { id: "n1" } },
      { channel: "notifications", type: "notification.new", payload: { id: "n1" } },
    ]);
  });

  it("unsubscribing one of two subscribers leaves the channel active for the other", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();

    const received: unknown[] = [];
    const unsubscribeA = manager.subscribe("notifications", (msg) => received.push({ who: "a", msg }));
    manager.subscribe("notifications", (msg) => received.push({ who: "b", msg }));

    unsubscribeA();
    const unsubscribeMessages = socket.sent.filter((s) => JSON.parse(s).type === "unsubscribe");
    expect(unsubscribeMessages).toHaveLength(0);

    socket.message({ channel: "notifications", type: "notification.new" });
    expect(received).toEqual([{ who: "b", msg: { channel: "notifications", type: "notification.new" } }]);
  });

  it("keeps two subscriptions independent even when they share the identical handler reference", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();

    const received: unknown[] = [];
    const sharedHandler = (msg: unknown) => received.push(msg);
    const unsubscribeA = manager.subscribe("notifications", sharedHandler);
    manager.subscribe("notifications", sharedHandler);

    unsubscribeA();
    const unsubscribeMessages = socket.sent.filter((s) => JSON.parse(s).type === "unsubscribe");
    expect(unsubscribeMessages).toHaveLength(0);

    socket.message({ channel: "notifications", type: "notification.new" });
    expect(received).toHaveLength(1);
  });

  it("sends an unsubscribe message once the last subscriber leaves", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();
    socket.open();

    const unsubscribeA = manager.subscribe("notifications", () => {});
    const unsubscribeB = manager.subscribe("notifications", () => {});

    unsubscribeA();
    unsubscribeB();

    const unsubscribeMessages = socket.sent.filter((s) => JSON.parse(s).type === "unsubscribe");
    expect(unsubscribeMessages).toHaveLength(1);
  });

  it("re-subscribes every active channel after a reconnect", () => {
    const manager = testManager();
    manager.connect();
    manager.subscribe("notifications", () => {});
    latestSocket().open();

    latestSocket().serverClose(1006, "abnormal");
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.instances).toHaveLength(2);

    latestSocket().open();
    const subscribeMessages = latestSocket().sent.filter((s) => JSON.parse(s).type === "subscribe");
    expect(subscribeMessages).toHaveLength(1);
  });

  it("reconnects with exponential backoff, capped at the max delay", () => {
    const manager = testManager();
    manager.connect();

    latestSocket().serverClose(1006);
    expect(FakeSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(2);

    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(3);
  });

  it("resets the backoff delay after a successful reconnect", () => {
    const manager = testManager();
    manager.connect();

    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.instances).toHaveLength(2);
    latestSocket().open();

    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(3);
  });

  it("does not reconnect after a 4001 auth-failure close", () => {
    const manager = testManager();
    manager.connect();

    latestSocket().serverClose(4001, "session expired");
    vi.advanceTimersByTime(60_000);

    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("disconnect() closes the connection and clears subscriptions without scheduling a reconnect", () => {
    const manager = testManager();
    manager.connect();
    manager.subscribe("notifications", () => {});
    const socket = latestSocket();

    manager.disconnect();

    expect(socket.closed).toBe(true);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("ignores a close event from an already-superseded socket", () => {
    const manager = testManager();
    manager.connect();
    const first = latestSocket();

    manager.connect();
    expect(FakeSocket.instances).toHaveLength(2);

    first.serverClose(1006);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(2);
  });

  it("closes a still-open prior connection when connect() is called again, without a spurious reconnect", () => {
    const manager = testManager();
    manager.connect();
    const first = latestSocket();
    first.open();

    manager.connect();

    expect(first.closed).toBe(true);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(2);
  });

  it("does not throw subscribing before the connection has opened, and sends once it does", () => {
    const manager = testManager();
    manager.connect();

    expect(() => manager.subscribe("notifications", () => {})).not.toThrow();
    expect(latestSocket().sent).toHaveLength(0);

    latestSocket().open();
    const subscribeMessages = latestSocket().sent.filter((s) => JSON.parse(s).type === "subscribe");
    expect(subscribeMessages).toHaveLength(1);
  });

  it("does not throw unsubscribing before the connection has opened", () => {
    const manager = testManager();
    manager.connect();
    const unsubscribe = manager.subscribe("notifications", () => {});

    expect(() => unsubscribe()).not.toThrow();
  });

  it("invokes onAuthFailure on a 4001 close instead of reconnecting", () => {
    const manager = testManager();
    const onAuthFailure = vi.fn();
    manager.onAuthFailure = onAuthFailure;
    manager.connect();

    latestSocket().serverClose(4001, "session expired");

    expect(onAuthFailure).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(1);
  });
});

describe("wireWebSocketManager", () => {
  // Wire before authentication, matching the singleton's bootstrap order.
  function wiredAndAuthenticated(): { machine: AuthMachine; manager: WebSocketManager } {
    const machine = new AuthMachine();
    const manager = testManager();
    wireWebSocketManager(machine, manager);
    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    return { machine, manager };
  }

  it("connects when the machine enters authenticated via login", () => {
    const machine = new AuthMachine();
    const manager = testManager();
    wireWebSocketManager(machine, manager);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("disconnects on logout", () => {
    const { machine } = wiredAndAuthenticated();
    const socket = latestSocket();

    machine.transition({ type: "logout_started" });

    expect(socket.closed).toBe(true);
  });

  it("re-opens the connection on the next successful login after logout", () => {
    const machine = new AuthMachine();
    const manager = testManager();
    wireWebSocketManager(machine, manager);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    machine.transition({ type: "logout_started" });
    machine.transition({ type: "logout_complete" });
    expect(FakeSocket.instances).toHaveLength(1);

    machine.transition({ type: "login_started" });
    machine.transition({ type: "login_succeeded", user, tenant });
    expect(FakeSocket.instances).toHaveLength(2);
  });

  it("does not disconnect or reconnect across a token refresh cycle", () => {
    const { machine } = wiredAndAuthenticated();
    expect(FakeSocket.instances).toHaveLength(1);
    const socket = latestSocket();

    machine.transition({ type: "refresh_started" });
    machine.transition({ type: "refresh_succeeded" });

    expect(socket.closed).toBe(false);
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("disconnects when a refresh fails and the session ends", () => {
    const { machine } = wiredAndAuthenticated();
    const socket = latestSocket();

    machine.transition({ type: "refresh_started" });
    machine.transition({ type: "refresh_failed" });

    expect(socket.closed).toBe(true);
  });

  it("ends the session as expired on a 4001 close", () => {
    const { machine } = wiredAndAuthenticated();

    latestSocket().serverClose(4001, "session expired");

    expect(machine.getState()).toMatchObject({ status: "unauthenticated", sessionExpired: true });
  });
});

it("keeps a restricted session disconnected until its password change is accepted", () => {
  const machine = new AuthMachine();
  const manager = wireWebSocketManager(machine, testManager());
  machine.transition({ type: "check_session" });
  machine.transition({ type: "session_checked", user: { ...user, passwordChangeRequired: true }, tenant });
  expect(FakeSocket.instances).toHaveLength(0);
  machine.transition({ type: "session_reloaded", user, tenant });
  expect(FakeSocket.instances).toHaveLength(1);
  machine.transition({ type: "password_change_required" });
  expect(latestSocket().closed).toBe(true);
  manager.disconnect();
});
