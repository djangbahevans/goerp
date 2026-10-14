import { act, cleanup, renderHook } from "@testing-library/react";
import { type ReactNode, StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authMachine } from "../auth/auth-machine.js";
import { useAuth } from "../auth/use-auth.js";
import { resourceRef } from "../names.js";
import { ShellProvider } from "../testing/shell-provider.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { usePresence } from "./use-presence.js";
import { wsManager } from "./ws-manager.js";

class Socket {
  static instances: Socket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  sent: { type: string; channel: string; field?: string | null }[] = [];
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
const contact = resourceRef<{ id: string; name: string; email: string }>("contacts.contact");
const channel = "record:contacts.contact:c1";
const ama = {
  user_id: "u1",
  name: "Ama",
  avatar_url: null,
  editing_field: null,
  joined_at: "2026-10-14T09:00:00Z",
  last_seen_at: "2026-10-14T09:00:30Z",
};
function update(users: unknown[], target = channel, resource = "contacts.contact", id = "c1") {
  act(() =>
    simulateRealtimeMessage(target, {
      type: "custom",
      event: "presence.update",
      payload: { resource, record_id: id, users },
    }),
  );
}

beforeEach(() => {
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

describe("usePresence", () => {
  it("maps full join, focus and leave snapshots, including dates and the current user", () => {
    const { result } = renderHook(() => usePresence(contact, "c1"));
    expect(result.current).toEqual([]);
    expect(socket().sent).toEqual([
      { type: "subscribe", channel },
      { type: "presence.join", channel },
    ]);
    update([ama]);
    expect(result.current).toEqual([
      {
        userId: "u1",
        name: "Ama",
        avatarUrl: null,
        editingField: null,
        joinedAt: new Date(ama.joined_at),
        lastSeenAt: new Date(ama.last_seen_at),
      },
    ]);
    const kwame = { ...ama, user_id: "u2", name: "Kwame", avatar_url: "/avatar/u2", editing_field: "email" };
    update([ama, kwame]);
    expect(result.current.map((user) => user.userId)).toEqual(["u1", "u2"]);
    expect(result.current[1]).toMatchObject({ avatarUrl: "/avatar/u2", editingField: "email" });
    update([{ ...kwame, editing_field: "name" }]);
    expect(result.current).toHaveLength(1);
    expect(result.current[0]?.editingField).toBe("name");
    update([]);
    expect(result.current).toEqual([]);
  });

  it("shares the latest snapshot with later consumers and leaves after the last unmount", () => {
    const first = renderHook(() => usePresence(contact, "c1"));
    update([ama]);
    const second = renderHook(() => usePresence(contact, "c1"));
    expect(second.result.current).toEqual(first.result.current);
    first.unmount();
    expect(socket().sent).toHaveLength(2);
    second.unmount();
    expect(socket().sent.slice(2)).toEqual([
      { type: "presence.leave", channel },
      { type: "unsubscribe", channel },
    ]);
    const fresh = renderHook(() => usePresence(contact, "c1"));
    expect(fresh.result.current).toEqual([]);
  });

  it("reports field changes without rejoining and restores the remaining consumer's focus", () => {
    const editor = renderHook(
      ({ field }: { field: "name" | "email" | null | undefined }) =>
        usePresence(contact, "c1", field === undefined ? {} : { editingField: field }),
      { initialProps: { field: "name" as "name" | "email" | null | undefined } },
    );
    const observer = renderHook(() => usePresence(contact, "c1"));
    expect(socket().sent.at(-1)).toEqual({ type: "presence.focus", channel, field: "name" });
    editor.rerender({ field: "email" });
    editor.rerender({ field: null });
    expect(socket().sent.slice(2)).toEqual([
      { type: "presence.focus", channel, field: "name" },
      { type: "presence.focus", channel, field: "email" },
      { type: "presence.focus", channel, field: null },
    ]);
    editor.rerender({ field: "name" });
    const other = renderHook(() => usePresence(contact, "c1", { editingField: "email" }));
    other.unmount();
    expect(socket().sent.at(-1)).toEqual({ type: "presence.focus", channel, field: "name" });
    editor.rerender({ field: undefined });
    expect(socket().sent.at(-1)).toEqual({ type: "presence.focus", channel, field: null });
    observer.unmount();
    editor.unmount();
  });

  it("clears the old list when resource or id changes and ignores stale and malformed messages", () => {
    const live = renderHook(({ resource, id }) => usePresence(resource, id), {
      initialProps: { resource: "contacts.contact", id: "c1" },
    });
    update([ama]);
    const previous = live.result.current;
    update([ama], "record:contacts.contact:other");
    update([ama], channel, "contacts.contact", "wrong");
    update([{ ...ama, joined_at: "bad date" }]);
    update([{ ...ama, editing_field: 123 }]);
    act(() => simulateRealtimeMessage(channel, { type: "custom", event: "presence.update", payload: null }));
    act(() =>
      simulateRealtimeMessage(channel, { type: "record.updated", resource: "contacts.contact", recordId: "c1" }),
    );
    expect(live.result.current).toBe(previous);
    live.rerender({ resource: "contacts.contact", id: "c2" });
    expect(live.result.current).toEqual([]);
    update([ama]);
    expect(live.result.current).toEqual([]);
    update([ama], "record:contacts.contact:c2", "contacts.contact", "c2");
    expect(live.result.current).toHaveLength(1);
    live.rerender({ resource: "sales.order", id: "c2" });
    expect(live.result.current).toEqual([]);
  });

  it.each(["subscription.denied", "record.unavailable"])("clears presence and stops rejoining after %s", (event) => {
    const live = renderHook(() => usePresence(contact, "c1"));
    update([ama]);
    act(() => simulateRealtimeMessage(channel, { type: "custom", event }));
    expect(live.result.current).toEqual([]);
    update([ama]);
    expect(live.result.current).toEqual([]);
    act(() => {
      socket().close();
      wsManager.connect();
      socket().open();
    });
    expect(socket().sent).toEqual([]);
  });

  it("rejoins and restores the latest focus after reconnect while awaiting a fresh snapshot", () => {
    const live = renderHook(
      ({ field }: { field: "name" | "email" }) => usePresence(contact, "c1", { editingField: field }),
      { initialProps: { field: "name" as "name" | "email" } },
    );
    update([ama]);
    act(() => socket().close());
    live.rerender({ field: "email" });
    act(() => {
      wsManager.connect();
      socket().open();
    });
    expect(socket().sent).toEqual([
      { type: "subscribe", channel },
      { type: "presence.join", channel },
      { type: "presence.focus", channel, field: "email" },
    ]);
    expect(live.result.current).toEqual([]);
    update([ama]);
    expect(live.result.current).toHaveLength(1);
  });

  it("cleans up strict-mode mounts without losing the active room", () => {
    const wrapper = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>;
    const live = renderHook(() => usePresence(contact, "c1", { editingField: "name" }), { wrapper });
    update([ama]);
    expect(live.result.current).toHaveLength(1);
    live.unmount();
    expect(socket().sent.at(-2)).toEqual({ type: "presence.leave", channel });
    expect(socket().sent.at(-1)).toEqual({ type: "unsubscribe", channel });
  });

  it("clears expired-session data and rejoins a mounted record after sign-in", () => {
    const fixture = renderHook(useAuth, {
      wrapper: ({ children }) => <ShellProvider module="contacts">{children}</ShellProvider>,
    });
    const { user, tenant } = fixture.result.current;
    if (!user || !tenant) throw new Error("missing test session");
    fixture.unmount();
    authMachine.transition({ type: "check_session" });
    authMachine.transition({ type: "session_checked", user, tenant });
    socket().open();

    const live = renderHook(() => usePresence(contact, "c1", { editingField: "name" }));
    update([ama]);
    act(() => authMachine.transition({ type: "refresh_started" }));
    expect(live.result.current).toHaveLength(1);
    act(() => authMachine.transition({ type: "refresh_failed" }));
    expect(live.result.current).toEqual([]);
    update([ama]);
    expect(live.result.current).toEqual([]);

    act(() => {
      authMachine.transition({ type: "login_started" });
      authMachine.transition({ type: "login_succeeded", user, tenant });
      socket().open();
    });
    expect(socket().sent).toEqual([
      { type: "subscribe", channel },
      { type: "presence.join", channel },
      { type: "presence.focus", channel, field: "name" },
    ]);
    expect(live.result.current).toEqual([]);
    update([ama]);
    expect(live.result.current).toHaveLength(1);
    act(() => authMachine.transition({ type: "logout_started" }));
    expect(live.result.current).toEqual([]);
    authMachine.transition({ type: "logout_complete" });
  });
});
