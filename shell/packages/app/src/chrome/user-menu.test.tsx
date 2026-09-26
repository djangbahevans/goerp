import { apiClient } from "@goerp/sdk";
import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { themeStore } from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addDays, todayIn } from "../activities/activity-dates.js";
import { onKeyboardShortcutsOpenRequest } from "../shortcuts/keyboard-shortcuts-control.js";
import { UserMenu } from "./user-menu.js";

function fakeAuth(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  const user = {
    id: "u1",
    email: "jane.doe@example.com",
    name: null,
    contactId: null,
    avatarUrl: null,
    roles: [],
    amr: [],
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
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: vi.fn(),
    logout: vi.fn(async () => {}),
    submitMFA: vi.fn(),
    updateProfile: vi.fn(),
    updatePreferences: vi.fn(async () => {}),
    changePassword: vi.fn(),
    reloadSession: vi.fn(),
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
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

// GET /_meta/scheduled-activities/mine answering with one page of
// activities due on the given dates.
function mockMyActivities(dueDates: string[]) {
  const data = dueDates.map((due_date, i) => ({
    id: `s${i}`,
    model: "sales.order",
    record_id: "o1",
    type: "call",
    summary: `activity ${i}`,
    note: null,
    due_date,
    assignee: { id: "u1", name: null, avatar_url: null },
    created_by: { id: "u1", name: null, avatar_url: null },
    created_at: "2026-09-24T10:00:00Z",
    done_at: null,
    done_by: null,
    feedback: null,
    record_name: "SO-0001",
  }));
  return vi.spyOn(apiClient, "get").mockResolvedValue({ data, meta: { cursor: null, has_more: false } } as never);
}

beforeEach(() => {
  window.localStorage.clear();
  mockMyActivities([]);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("UserMenu", () => {
  it("derives a display name from the user's email for the avatar and trigger label", async () => {
    await renderUserMenu();
    expect(screen.getByRole("button", { name: "Jane Doe's account menu" })).toBeTruthy();
  });

  it("lists My activities, Profile, Settings, Dark mode, Keyboard shortcuts, and Sign out", async () => {
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));

    expect(screen.getByRole("menuitem", { name: "My activities" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Profile" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Settings" })).toBeTruthy();
    expect(screen.getByRole("menuitemcheckbox", { name: "Dark mode" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Keyboard shortcuts" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeTruthy();
  });

  it("badges My activities with the overdue plus due-today count, and it navigates to /activities", async () => {
    const today = todayIn("UTC");
    mockMyActivities([addDays(today, -3), addDays(today, -1), today, addDays(today, 1), addDays(today, 7)]);
    const router = await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));

    await waitFor(() =>
      expect(screen.getByRole("menuitem", { name: /My activities/ }).textContent).toBe("My activities3"),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: /My activities/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/activities"));
  });

  it("shows Admin only to a tenant admin, and it navigates to /admin", async () => {
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    expect(screen.queryByRole("menuitem", { name: "Admin" })).toBeNull();
    cleanup();

    const base = fakeAuth();
    const admin = { ...base.user!, roles: ["admin"] };
    const router = await renderUserMenu(fakeAuth({ user: admin }));
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Admin" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin"));
  });

  it("Keyboard shortcuts opens the shortcuts reference", async () => {
    const onOpen = vi.fn();
    const unsubscribe = onKeyboardShortcutsOpenRequest(onOpen);
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Keyboard shortcuts" }));
    unsubscribe();

    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("toggling Dark mode flips its checked state without closing the menu", async () => {
    await renderUserMenu();
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }));

    expect(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }).getAttribute("aria-checked")).toBe("true");
  });

  it("saves the toggled theme to the profile", async () => {
    themeStore.setPreference("light");
    const updatePreferences = vi.fn(async () => {});
    await renderUserMenu(fakeAuth({ updatePreferences }));
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }));

    expect(updatePreferences).toHaveBeenCalledWith({ theme: "dark" });
  });

  it("reverts the theme when saving it fails", async () => {
    themeStore.setPreference("light");
    const updatePreferences = vi.fn(async () => {
      throw new Error("offline");
    });
    await renderUserMenu(fakeAuth({ updatePreferences }));
    fireEvent.click(screen.getByRole("button", { name: "Jane Doe's account menu" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Dark mode" }));

    await waitFor(() => expect(themeStore.getPreference()).toBe("light"));
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
    const user = {
      id: "u2",
      email: "@example.com",
      name: null,
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
    };
    await renderUserMenu(fakeAuth({ user }));
    expect(screen.getByRole("button", { name: "@example.com's account menu" })).toBeTruthy();
  });

  it("falls back to a derived name when the profile name is an empty string", async () => {
    const user = {
      id: "u4",
      email: "jane.doe@example.com",
      name: "",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
    };
    await renderUserMenu(fakeAuth({ user }));
    expect(screen.getByRole("button", { name: "Jane Doe's account menu" })).toBeTruthy();
  });

  it("uses the real name instead of deriving one when the user has a profile", async () => {
    const user = {
      id: "u3",
      email: "jane.doe@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
    };
    await renderUserMenu(fakeAuth({ user }));
    expect(screen.getByRole("button", { name: "Ada Lovelace's account menu" })).toBeTruthy();
  });
});
