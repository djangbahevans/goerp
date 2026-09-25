import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, tenantSuspension } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
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
};
const FAKE_TENANT = { id: "t1", slug: "acme", name: "Acme Corp", plan: "pro" };
const SIGNED_IN: AuthContextValue = {
  state: { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT },
  isAuthenticated: true,
  user: FAKE_USER,
  tenant: FAKE_TENANT,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
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

function renderAt(path: string, auth: AuthContextValue) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createRouter({ routeTree, context: { auth }, history });
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={permissions}>
          <ViewRegistryContext.Provider value={buildEmptyViewRegistry()}>
            <AuthRouterProvider router={router} />
          </ViewRegistryContext.Provider>
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
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
});
