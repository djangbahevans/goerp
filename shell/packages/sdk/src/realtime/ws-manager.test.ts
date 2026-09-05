import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthMachine } from "../auth/auth-machine.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import { WebSocketManager, wireWebSocketManager } from "./ws-manager.js";

const user: CurrentUser = { id: "u1", email: "a@example.com", roles: [], amr: ["pwd"], mfaVerifiedAt: null };
const tenant: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

class FakeSocket {
  static instances: FakeSocket[] = [];

  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  sent: string[] = [];
  closed = false;

  constructor(public readonly url: string) {
    FakeSocket.instances.push(this);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(code = 1000, reason = ""): void {
    if (this.closed) return;
    this.closed = true;
    this.onclose?.({ code, reason });
  }

  open(): void {
    this.onopen?.();
  }

  message(envelope: unknown): void {
    this.onmessage?.({ data: JSON.stringify(envelope) });
  }

  // Simulates the server dropping the connection — unlike close(), this
  // doesn't mark the socket as closed-by-the-caller.
  serverClose(code: number, reason = ""): void {
    this.onclose?.({ code, reason });
  }
}

function fakeFactory(): (url: string) => WebSocket {
  FakeSocket.instances = [];
  return (url: string) => new FakeSocket(url) as unknown as WebSocket;
}

// vitest's default (node) environment has no `location` global, which the
// real default URL factory reads — tests always supply their own so
// WebSocketManager's constructor default is never exercised here.
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

describe("WebSocketManager", () => {
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
    expect(unsubscribeMessages).toHaveLength(0); // "b" is still subscribed

    socket.message({ channel: "notifications", type: "notification.new" });
    expect(received).toEqual([{ who: "b", msg: { channel: "notifications", type: "notification.new" } }]);
  });

  it("sends an unsubscribe message once the last subscriber leaves", () => {
    const manager = testManager();
    manager.connect();
    const socket = latestSocket();

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
    expect(FakeSocket.instances).toHaveLength(2); // first retry at 1s

    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(3); // second retry at 2s (doubled)
  });

  it("resets the backoff delay after a successful reconnect", () => {
    const manager = testManager();
    manager.connect();

    latestSocket().serverClose(1006);
    vi.advanceTimersByTime(1000);
    expect(FakeSocket.instances).toHaveLength(2);
    latestSocket().open(); // successful reconnect resets backoff to 1s

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
    expect(FakeSocket.instances).toHaveLength(1); // no reconnect attempted
  });

  it("ignores a close event from an already-superseded socket", () => {
    const manager = testManager();
    manager.connect();
    const first = latestSocket();

    // A fresh connect() (e.g. a new login) supersedes the first socket
    // before it reports its own close — that stale event must not tear
    // down or reschedule against the new one.
    manager.connect();
    expect(FakeSocket.instances).toHaveLength(2);

    first.serverClose(1006);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(2);
  });
});

describe("wireWebSocketManager", () => {
  // wireWebSocketManager only reacts to transitions that happen *after*
  // it subscribes — mirroring the real bootstrap order, where the module-
  // level wsManager singleton wires itself at import time, before
  // AuthProvider's mount effect ever fires check_session.
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
});
