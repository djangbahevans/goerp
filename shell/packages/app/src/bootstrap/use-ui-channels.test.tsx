import { toast } from "@goerp/sdk/notifications";
import { type UIInstruction, wsManager } from "@goerp/sdk/realtime";
import { simulateRealtimeMessage } from "@goerp/sdk/testing";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, type MockInstance, vi } from "vitest";
import { useUIChannels } from "./use-ui-channels.js";

const { auth } = vi.hoisted(() => ({
  auth: {
    isAuthenticated: true,
    user: { id: "u1", roles: ["sales_manager"] } as {
      id: string;
      roles: string[];
      passwordChangeRequired?: boolean;
    } | null,
    reloadSession: vi.fn(async () => {}),
  },
}));
vi.mock("@goerp/sdk/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@goerp/sdk/auth")>()),
  useAuth: () => auth,
}));

let info: MockInstance<typeof toast.info>;
let queryClient: QueryClient;

function renderUIChannels() {
  return renderHook(() => useUIChannels(), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  });
}

beforeEach(() => {
  queryClient = new QueryClient();
  auth.isAuthenticated = true;
  auth.user = { id: "u1", roles: ["sales_manager"] };
  auth.reloadSession.mockClear();
  info = vi.spyOn(toast, "info");
});

afterEach(() => {
  cleanup();
  toast.dismiss();
  vi.restoreAllMocks();
});

function push(channel: string, message: string) {
  const instructions: UIInstruction[] = [{ type: "toast", message, severity: "info" }];
  simulateRealtimeMessage(channel, { type: "custom", event: "ui.push", payload: { instructions } });
}

async function shown(): Promise<string[]> {
  await Promise.resolve();
  await Promise.resolve();
  return info.mock.calls.map(([message]) => message as string);
}

it("runs pushes on the user's channel and each role channel", async () => {
  auth.user = { id: "u1", roles: ["sales_manager", "warehouse"] };
  renderUIChannels();

  push("ui:user:u1", "user");
  push("ui:role:sales_manager", "sales");
  push("ui:role:warehouse", "warehouse");
  push("ui:role:accountant", "not held");

  expect(await shown()).toEqual(["user", "sales", "warehouse"]);
});

it("skips a pushed navigate with a warning and runs the rest", async () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  renderUIChannels();

  simulateRealtimeMessage("ui:user:u1", {
    type: "custom",
    event: "ui.push",
    payload: {
      instructions: [
        { type: "navigate", path: "/sales" },
        { type: "toast", message: "after", severity: "info" },
      ],
    },
  });

  expect(await shown()).toEqual(["after"]);
  expect(warn).toHaveBeenCalledWith("ui: navigate can't be pushed; skipped");
});

it("reloads the session on role.changed and follows the new roles", async () => {
  const { rerender } = renderUIChannels();

  simulateRealtimeMessage("ui:user:u1", {
    type: "custom",
    event: "role.changed",
    payload: { role: "warehouse", action: "granted" },
  });
  expect(auth.reloadSession).toHaveBeenCalledOnce();

  auth.user = { id: "u1", roles: ["warehouse"] };
  rerender();
  push("ui:role:sales_manager", "revoked");
  push("ui:role:warehouse", "granted");

  expect(await shown()).toEqual(["granted"]);
});

it("keeps a role's subscription when the session reloads with the same roles", async () => {
  const { rerender } = renderUIChannels();
  auth.user = { id: "u1", roles: ["sales_manager"] };
  rerender();

  push("ui:role:sales_manager", "once");

  expect(await shown()).toEqual(["once"]);
});

it("unsubscribes everything on sign-out", async () => {
  const { rerender } = renderUIChannels();
  auth.isAuthenticated = false;
  auth.user = null;
  rerender();

  push("ui:user:u1", "user");
  push("ui:role:sales_manager", "role");

  expect(await shown()).toEqual([]);
});

it("reloads the session when the socket reconnects, to catch role changes missed while down", () => {
  const reconnectHandlers: (() => void)[] = [];
  vi.spyOn(wsManager, "subscribeReconnect").mockImplementation((handler) => {
    reconnectHandlers.push(() => handler(new Set()));
    return () => {};
  });
  renderUIChannels();

  for (const onReconnect of reconnectHandlers) onReconnect();

  expect(auth.reloadSession).toHaveBeenCalledOnce();
});

it("subscribes nothing while the session must change its password", async () => {
  auth.user = { id: "u1", roles: ["sales_manager"], passwordChangeRequired: true };
  renderUIChannels();

  push("ui:user:u1", "user");
  push("ui:role:sales_manager", "role");

  expect(await shown()).toEqual([]);
});

const countKey = ["notifications", "unread-count"];

function pushReadAll(): void {
  simulateRealtimeMessage("notifications", { type: "custom", event: "notification.read_all" });
}

it("keeps the notification count current from the notifications channel while signed in", () => {
  queryClient.setQueryData(countKey, { count: 3 });
  renderUIChannels();

  pushReadAll();

  expect(queryClient.getQueryData(countKey)).toEqual({ count: 0 });
});

it("subscribes to no notifications while signed out, and drops the subscription on sign-out", () => {
  const subscribe = vi.spyOn(wsManager, "subscribe");
  auth.isAuthenticated = false;
  auth.user = null;
  queryClient.setQueryData(countKey, { count: 3 });
  const { rerender } = renderUIChannels();

  pushReadAll();
  expect(subscribe).not.toHaveBeenCalledWith("notifications", expect.any(Function));
  expect(queryClient.getQueryData(countKey)).toEqual({ count: 3 });

  auth.isAuthenticated = true;
  auth.user = { id: "u1", roles: [] };
  rerender();
  auth.isAuthenticated = false;
  auth.user = null;
  rerender();

  pushReadAll();
  expect(subscribe).toHaveBeenCalledWith("notifications", expect.any(Function));
  expect(queryClient.getQueryData(countKey)).toEqual({ count: 3 });
});
