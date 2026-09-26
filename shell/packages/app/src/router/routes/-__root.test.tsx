import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, tenantSuspension } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const FAKE_USER = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
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
const FAKE_TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme Corp",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};
const SIGNED_IN: AuthContextValue = {
  state: { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT },
  isAuthenticated: true,
  user: FAKE_USER,
  tenant: FAKE_TENANT,
  login: async () => null,
  completeHandoff: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};
const SIGNED_OUT: AuthContextValue = {
  ...SIGNED_IN,
  state: { status: "unauthenticated" },
  isAuthenticated: false,
  user: null,
  tenant: null,
};
const EXPIRED: AuthContextValue = {
  ...SIGNED_OUT,
  state: { status: "unauthenticated", sessionExpired: true, user: FAKE_USER, tenant: FAKE_TENANT },
  user: FAKE_USER,
  tenant: FAKE_TENANT,
};

// setAuth swaps the live auth value in place, the way AuthProvider does
// when the machine transitions.
function mount(path: string, auth: AuthContextValue) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createRouter({ routeTree, context: { auth }, history });
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const ui = (value: AuthContextValue) => (
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={value}>
        <PermissionContext.Provider value={permissions}>
          <ViewRegistryContext.Provider value={buildEmptyViewRegistry()}>
            <AuthRouterProvider router={router} />
          </ViewRegistryContext.Provider>
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
  const { rerender } = render(ui(auth));
  return { router, setAuth: (value: AuthContextValue) => rerender(ui(value)) };
}

function renderAt(path: string, auth: AuthContextValue) {
  return mount(path, auth).router;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  tenantSuspension.set(false);
});

const hasChrome = () => screen.queryByRole("navigation", { name: "Main" }) !== null;

describe("root layout", () => {
  it("renders an app route inside the chrome", async () => {
    renderAt("/", SIGNED_IN);

    expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();
    expect(screen.getByRole("banner")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Skip to content" })).toBeTruthy();
  });

  it("holds a user who must enroll in MFA on the setup wizard", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ enrollment_id: "e1", qr_svg: "<svg/>", secret: "ABCD" }))),
    );
    const needsSetup = { ...FAKE_USER, mfaSetupRequired: true };
    const router = renderAt("/settings/profile?tab=a", {
      ...SIGNED_IN,
      state: { status: "authenticated", user: needsSetup, tenant: FAKE_TENANT },
      user: needsSetup,
    });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa-setup"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile?tab=a" });
    expect(await screen.findByRole("heading", { name: /requires two-factor authentication/ })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  it("renders an /auth/* route with no sidebar or header", async () => {
    renderAt("/auth/login", SIGNED_OUT);

    expect(await screen.findByRole("button", { name: /sign in/i })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
    expect(screen.queryByRole("banner")).toBeNull();
    expect(screen.queryByRole("link", { name: "Skip to content" })).toBeNull();
  });

  it("renders an unmatched URL as the 404 page in place, with no chrome", async () => {
    const router = renderAt("/no/such/page", SIGNED_IN);

    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/no/such/page");
    expect(hasChrome()).toBe(false);
  });

  it.each([
    ["/404", "Page not found"],
    ["/403", "You don't have access to this page"],
  ])("renders %s with no chrome", async (path, heading) => {
    renderAt(path, SIGNED_IN);

    expect(await screen.findByRole("heading", { name: heading })).toBeTruthy();
    expect(hasChrome()).toBe(false);
    expect(screen.queryByRole("banner")).toBeNull();
  });

  it("sends a signed-out visitor to the tenant-suspended page once a 403 tenant_suspended arrives", async () => {
    const router = renderAt("/auth/login", SIGNED_OUT);
    expect(await screen.findByRole("button", { name: /sign in/i })).toBeTruthy();

    act(() => tenantSuspension.set(true));

    await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
    expect(await screen.findByRole("heading", { name: "This organisation's account has been suspended" })).toBeTruthy();
    expect(hasChrome()).toBe(false);
  });

  it("holds a signed-in user of a suspended tenant on the tenant-suspended page", async () => {
    tenantSuspension.set(true);
    const router = renderAt("/settings/profile", SIGNED_IN);

    await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
    expect(hasChrome()).toBe(false);
  });

  describe("session expired", () => {
    const expiredDialog = () => screen.queryByRole("alertdialog", { name: "Your session has expired" });

    it("shows the modal over the current page, with no navigation, when the session expires", async () => {
      const { router, setAuth } = mount("/", SIGNED_IN);
      expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();

      act(() => setAuth(EXPIRED));

      expect(await screen.findByRole("alertdialog", { name: "Your session has expired" })).toBeTruthy();
      expect(screen.getByText("Please sign in again to continue.")).toBeTruthy();
      expect(router.state.location.pathname).toBe("/");
      // The page is still there behind the modal, hidden from assistive tech.
      expect(screen.getByRole("navigation", { name: "Main", hidden: true })).toBeTruthy();
      expect(hasChrome()).toBe(false);
      // Queries behind the modal stop refetching on focus and polling.
      expect(focusManager.isFocused()).toBe(false);
    });

    it("can't be dismissed and keeps focus inside the modal", async () => {
      renderAt("/", EXPIRED);
      const dialog = await screen.findByRole("alertdialog", { name: "Your session has expired" });
      await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));

      fireEvent.keyDown(dialog, { key: "Escape" });
      fireEvent.pointerDown(document.body);

      expect(expiredDialog()).toBeTruthy();
      expect(screen.queryByRole("button", { name: /close|cancel/i })).toBeNull();
    });

    it("signs in again at the login page, carrying the page it was shown over", async () => {
      const router = renderAt("/?tab=open", EXPIRED);

      fireEvent.click(await screen.findByRole("button", { name: "Sign in again" }));

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(router.state.location.search).toEqual({ redirect: "/?tab=open" });
      await waitFor(() => expect(expiredDialog()).toBeNull());
      expect(focusManager.isFocused()).toBe(true);
    });

    it("redirects a signed-out visitor to login with no modal", async () => {
      const router = renderAt("/", SIGNED_OUT);

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("redirects to login with no modal after signing out", async () => {
      const { router, setAuth } = mount("/", SIGNED_IN);
      expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();

      act(() => setAuth(SIGNED_OUT));

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("sends a session that expires on an auth page to login instead of holding it there", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(JSON.stringify({ enrollment_id: "e1", qr_svg: "<svg/>", secret: "ABCD" }))),
      );
      const needsSetup = { ...FAKE_USER, mfaSetupRequired: true };
      const { router, setAuth } = mount("/auth/mfa-setup", {
        ...SIGNED_IN,
        state: { status: "authenticated", user: needsSetup, tenant: FAKE_TENANT },
        user: needsSetup,
      });
      expect(await screen.findByRole("heading", { name: /requires two-factor authentication/ })).toBeTruthy();

      act(() =>
        setAuth({
          ...EXPIRED,
          state: { status: "unauthenticated", sessionExpired: true, user: needsSetup, tenant: FAKE_TENANT },
          user: needsSetup,
        }),
      );

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("leaves a suspended tenant's page to its own sign-in flow", async () => {
      tenantSuspension.set(true);
      const router = renderAt("/", EXPIRED);

      await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
      expect(await screen.findByRole("button", { name: "Sign in with a different account" })).toBeTruthy();
      expect(expiredDialog()).toBeNull();
    });
  });
});
