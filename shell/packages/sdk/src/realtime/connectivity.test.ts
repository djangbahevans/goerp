import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Connectivity, SOCKET_GRACE_MS, wireConnectivity } from "./connectivity.js";
import type { SocketStatus, SocketStatusListener } from "./ws-manager.js";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

function fakeWindow(onLine = true) {
  const handlers = new Map<string, () => void>();
  return {
    navigator: { onLine },
    addEventListener: (type: string, handler: () => void) => handlers.set(type, handler),
    fire: (type: "online" | "offline") => handlers.get(type)?.(),
  };
}

function fakeManager(initial: SocketStatus = "closed") {
  let listener: SocketStatusListener = () => {};
  return {
    getStatus: () => initial,
    subscribeStatus: (next: SocketStatusListener) => {
      listener = next;
      return () => {};
    },
    reconnectNow: vi.fn(),
    emit: (status: SocketStatus) => listener(status),
  };
}

function setup(onLine = true) {
  const target = fakeWindow(onLine);
  const manager = fakeManager();
  const connectivity = wireConnectivity(new Connectivity(), manager, target as unknown as Window);
  const changes: boolean[] = [];
  connectivity.subscribe((online) => changes.push(online));
  return { target, manager, connectivity, changes };
}

describe("Connectivity", () => {
  it("goes offline at once when the browser reports offline, and back once it is online with the socket open", () => {
    const { target, manager, connectivity } = setup();
    manager.emit("open");

    target.fire("offline");
    expect(connectivity.get()).toBe(false);

    manager.emit("reconnecting");
    vi.advanceTimersByTime(SOCKET_GRACE_MS);
    target.fire("online");
    expect(connectivity.get()).toBe(false);

    manager.emit("open");
    expect(connectivity.get()).toBe(true);
  });

  it("starts offline when the browser already reports offline", () => {
    expect(setup(false).connectivity.get()).toBe(false);
  });

  it("goes offline once the socket has been down for the grace period", () => {
    const { manager, connectivity, changes } = setup();
    manager.emit("open");
    manager.emit("reconnecting");

    vi.advanceTimersByTime(SOCKET_GRACE_MS - 1);
    expect(connectivity.get()).toBe(true);
    vi.advanceTimersByTime(1);
    expect(connectivity.get()).toBe(false);

    manager.emit("open");
    expect(changes).toEqual([false, true]);
  });

  it("stays online when the socket reconnects within the grace period", () => {
    const { manager, changes } = setup();
    manager.emit("open");
    manager.emit("reconnecting");
    vi.advanceTimersByTime(SOCKET_GRACE_MS - 1);
    manager.emit("open");
    vi.advanceTimersByTime(SOCKET_GRACE_MS);

    expect(changes).toEqual([]);
  });

  it("ignores a closed socket: never opened, an auth failure or a logout", () => {
    const { manager, connectivity } = setup();
    manager.emit("closed");
    vi.advanceTimersByTime(SOCKET_GRACE_MS * 2);
    expect(connectivity.get()).toBe(true);

    manager.emit("reconnecting");
    vi.advanceTimersByTime(SOCKET_GRACE_MS);
    expect(connectivity.get()).toBe(false);
    manager.emit("closed");
    expect(connectivity.get()).toBe(true);
  });

  it("reconnects immediately when the browser comes back online", () => {
    const { target, manager } = setup();
    target.fire("offline");
    target.fire("online");
    expect(manager.reconnectNow).toHaveBeenCalledTimes(1);
  });
});
