import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CommandPalette } from "./command-palette.js";
import { commandRegistry } from "./command-registry.js";
import type { Command } from "./command-types.js";

function fakeAuth(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  const user = { id: "u1", email: "a@b.com", roles: [], amr: [], mfaVerifiedAt: null };
  const tenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: vi.fn(),
    logout: vi.fn(async () => {}),
    submitMFA: vi.fn(),
    ...overrides,
  };
}

function Providers({
  children,
  permissions = [],
  auth = fakeAuth(),
}: {
  children: ReactNode;
  permissions?: string[] | undefined;
  auth?: AuthContextValue | undefined;
}) {
  const queryClient = new QueryClient();
  const permissionValue = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={permissionValue}>{children}</PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

async function renderPalette(opts: { permissions?: string[]; auth?: AuthContextValue } = {}) {
  const rootRoute = createRootRoute({
    component: () => (
      <Providers permissions={opts.permissions} auth={opts.auth}>
        <CommandPalette />
      </Providers>
    ),
  });
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => null });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

function openPalette() {
  fireEvent.keyDown(document, { key: "k", metaKey: true });
}

const unregisterFns: Array<() => void> = [];
function registerTestCommands(commands: Command[]): void {
  unregisterFns.push(commandRegistry.register(commands));
}

beforeEach(() => {
  vi.spyOn(toast, "success").mockImplementation(() => {});
  // jsdom doesn't implement scrollIntoView at all.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  for (const unregister of unregisterFns.splice(0)) unregister();
});

describe("CommandPalette", () => {
  it("is not rendered until opened", async () => {
    await renderPalette();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens on Cmd/Ctrl+K from anywhere", async () => {
    await renderPalette();
    openPalette();
    expect(screen.getByRole("dialog", { name: "Command palette" })).toBeTruthy();
  });

  it("closes on Escape", async () => {
    await renderPalette();
    openPalette();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("filters registered commands as the user types, hiding non-matches", async () => {
    registerTestCommands([
      { id: "new-contact", label: "New Contact", action: vi.fn() },
      { id: "archive", label: "Archive Order", action: vi.fn() },
    ]);
    await renderPalette();
    openPalette();

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "contact" } });
    expect(screen.getByText("New Contact")).toBeTruthy();
    expect(screen.queryByText("Archive Order")).toBeNull();
  });

  it("hides a command the current user lacks permission for", async () => {
    registerTestCommands([{ id: "danger", label: "Danger Zone", permission: "admin:danger", action: vi.fn() }]);
    await renderPalette({ permissions: [] });
    openPalette();

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "danger" } });
    expect(screen.queryByText("Danger Zone")).toBeNull();
  });

  it("shows a permission-gated command once the user has that permission", async () => {
    registerTestCommands([{ id: "danger", label: "Danger Zone", permission: "admin:danger", action: vi.fn() }]);
    await renderPalette({ permissions: ["admin:danger"] });
    openPalette();

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "danger" } });
    expect(screen.getByText("Danger Zone")).toBeTruthy();
  });

  it("ArrowDown/ArrowUp move the highlighted option, clamping at the ends", async () => {
    registerTestCommands([
      { id: "a", label: "Alpha Command", action: vi.fn() },
      { id: "b", label: "Beta Command", action: vi.fn() },
    ]);
    await renderPalette();
    openPalette();
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "command" } });

    const options = () => screen.getAllByRole("option");
    expect(options()[0]?.getAttribute("aria-selected")).toBe("true");

    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(options()[1]?.getAttribute("aria-selected")).toBe("true");

    // Clamps at the last option instead of wrapping back to the first.
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(options()[1]?.getAttribute("aria-selected")).toBe("true");

    fireEvent.keyDown(input, { key: "ArrowUp" });
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(options()[0]?.getAttribute("aria-selected")).toBe("true");
  });

  it("Enter executes the highlighted command and closes the palette", async () => {
    const action = vi.fn();
    registerTestCommands([{ id: "a", label: "Alpha Command", action }]);
    await renderPalette();
    openPalette();
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "alpha" } });

    fireEvent.keyDown(input, { key: "Enter" });
    expect(action).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("clicking a result executes it the same way Enter does", async () => {
    const action = vi.fn();
    registerTestCommands([{ id: "a", label: "Alpha Command", action }]);
    await renderPalette();
    openPalette();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "alpha" } });

    fireEvent.click(screen.getByText("Alpha Command"));
    expect(action).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("passes navigate/toast/queryClient to the executed command", async () => {
    let received: unknown;
    registerTestCommands([
      {
        id: "a",
        label: "Alpha Command",
        action: (ctx) => {
          received = ctx;
        },
      },
    ]);
    await renderPalette();
    openPalette();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "alpha" } });
    fireEvent.click(screen.getByText("Alpha Command"));

    expect(received).toMatchObject({ navigate: expect.any(Function), toast, queryClient: expect.any(QueryClient) });
  });

  it("shows an inline no-results row for a query that matches nothing", async () => {
    registerTestCommands([{ id: "a", label: "Alpha Command", action: vi.fn() }]);
    await renderPalette();
    openPalette();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "zzz-no-match" } });

    expect(screen.getByText('No results for "zzz-no-match"')).toBeTruthy();
  });

  it("shows the built-in Sign Out command and calls logout when executed", async () => {
    const logout = vi.fn(async () => {});
    await renderPalette({ auth: fakeAuth({ logout }) });
    openPalette();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "sign out" } });

    fireEvent.click(screen.getByText("Sign Out"));
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("resets the query each time it's reopened", async () => {
    registerTestCommands([{ id: "a", label: "Alpha Command", action: vi.fn() }]);
    await renderPalette();
    openPalette();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "alpha" } });
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    openPalette();
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("");
  });
});
