import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../auth/auth-provider.js";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import type { AuthContextValue, CurrentUser } from "../auth/types.js";
import type { RealtimeMessage } from "../realtime/realtime-message.js";
import { wsManager } from "../realtime/ws-manager.js";
import { ModuleNavigationProvider, useModule } from "./use-module.js";

// A real WebSocketManager over a fake socket, as in the realtime tests.
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

vi.mock("../realtime/ws-manager.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../realtime/ws-manager.js")>();
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

function simulateRealtimeMessage(channel: string, type: string, payload?: unknown) {
  act(() => socket().onmessage?.({ data: JSON.stringify({ channel, type, payload }) }));
}

const user: CurrentUser = {
  id: "u1",
  email: "ada@acme.test",
  name: "Ada",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};
const auth: AuthContextValue = {
  state: { status: "authenticated", user, tenant },
  isAuthenticated: true,
  user,
  tenant,
  login: async () => null,
  completeHandoff: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

beforeEach(() => {
  wsManager.connect();
  socket().readyState = 1;
  socket().onopen?.();
});

afterEach(() => {
  cleanup();
  wsManager.disconnect();
});

describe("useModule().realtime", () => {
  it("delivers a channel's messages to a module component until it unmounts", () => {
    const received: RealtimeMessage[] = [];
    function ContactsFeed() {
      const { realtime } = useModule("contacts");
      useEffect(() => realtime.subscribe("feed:contacts", (message) => received.push(message)), [realtime]);
      return null;
    }
    const { unmount } = render(
      <QueryClientProvider client={new QueryClient()}>
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider
            value={createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() })}
          >
            <ModuleNavigationProvider navigate={vi.fn()}>
              <ContactsFeed />
            </ModuleNavigationProvider>
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>,
    );

    simulateRealtimeMessage("feed:contacts", "record.updated", { resource: "contacts.contact", record_id: "c1" });
    unmount();
    simulateRealtimeMessage("feed:contacts", "record.updated", { resource: "contacts.contact", record_id: "c2" });

    expect(received).toEqual([
      { type: "record.updated", event: "record.updated", resource: "contacts.contact", recordId: "c1" },
    ]);
    expect(socket().sent.map((frame) => `${frame.type} ${frame.channel}`)).toEqual([
      "subscribe feed:contacts",
      "unsubscribe feed:contacts",
    ]);
  });
});
