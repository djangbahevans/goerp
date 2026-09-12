import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { UserMenu } from "./user-menu.js";

function fakeAuth(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  const user = { id: "u1", email: "jane.doe@example.com", roles: [], amr: [], mfaVerifiedAt: null };
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

const permissionValue = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
});

async function renderUserMenu(auth: AuthContextValue = fakeAuth()) {
  const rootRoute = createRootRoute({
    component: () => (
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={permissionValue}>
          <UserMenu />
        </PermissionContext.Provider>
      </AuthContext.Provider>
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

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(cleanup);

describe("UserMenu", () => {
  it("derives a display name from the user's email for the avatar and trigger label", async () => {
    await renderUserMenu();
    expect(screen.getByRole("button", { name: "Jane Doe's account menu" })).toBeTruthy();
  });

  it("lists Profile, Settings, Dark mode, Keyboard shortcuts, and Sign out", async () => {
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));

    expect(screen.getByRole("menuitem", { name: "Profile" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Settings" })).toBeTruthy();
    expect(screen.getByRole("menuitemcheckbox", { name: "Dark mode" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Keyboard shortcuts" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeTruthy();
  });

  it("toggling Dark mode flips its checked state without closing the menu", async () => {
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }));

    expect(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }).getAttribute("aria-checked")).toBe("true");
  });

  it("Sign out calls logout()", async () => {
    const logout = vi.fn(async () => {});
    await renderUserMenu(fakeAuth({ logout }));
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Sign out" }));

    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("renders nothing when there is no signed-in user", async () => {
    await renderUserMenu(fakeAuth({ user: null }));
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("falls back to the raw email when the local-part title-cases to empty", async () => {
    const user = { id: "u2", email: "@example.com", roles: [], amr: [], mfaVerifiedAt: null };
    await renderUserMenu(fakeAuth({ user }));
    expect(screen.getByRole("button", { name: "@example.com's account menu" })).toBeTruthy();
  });
});
