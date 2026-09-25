import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import type { ResolvedView, ViewRegistry } from "@goerp/sdk/schema";
import { buildEmptyViewRegistry, viewRegistryRef } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

// Matches command-palette.test.tsx's own fakeAuth() shape — __root.tsx's
// component always renders CommandPalette (useAuth + useQueryClient +
// useContext(PermissionContext)) regardless of which child route matches,
// so every route test needs these three, not just this route's own logic.
const FAKE_USER = {
  id: "u1",
  email: "a@b.com",
  contactId: null,
  name: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
};
const FAKE_TENANT = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };
const FAKE_AUTH: AuthContextValue = {
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

// Exercises the real generated route tree (not a hand-built stand-in) so
// the bracket-escaped filename ([_m].$.tsx) is actually proven to resolve
// to "/_m/*", not just asserted in a comment — see this route's own doc
// comment for why the escape is load-bearing. The permission value here
// mirrors permissionDataRef so both stay consistent within one test.
async function renderAt(path: string) {
  const router = createRouter({
    routeTree,
    context: { auth: FAKE_AUTH },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={FAKE_AUTH}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

function registryResolvingTo(path: string, view: ResolvedView): ViewRegistry {
  return { ...buildEmptyViewRegistry(), resolveRoute: (p) => (p === path ? view : null) };
}

const LIST_VIEW: ResolvedView = {
  module: "contacts",
  viewName: "contacts_list",
  viewType: "list",
  declaration: { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" },
  permissions: [],
  bundleUrl: null,
};

afterEach(() => {
  cleanup();
  viewRegistryRef.current = buildEmptyViewRegistry();
  permissionDataRef.current = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };
});

describe("/_m/$ catch-all route", () => {
  it("renders the not-found page for a path nothing resolves", async () => {
    viewRegistryRef.current = buildEmptyViewRegistry();
    const router = await renderAt("/_m/nonexistent");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/nonexistent");
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  it("redirects to the module-not-enabled /403 when the resolved view's module isn't enabled", async () => {
    viewRegistryRef.current = registryResolvingTo("/contacts", LIST_VIEW);
    permissionDataRef.current = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };

    const router = await renderAt("/_m/contacts");

    expect(await screen.findByRole("heading", { name: "This module isn't enabled for your account" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/403");
    expect(router.state.location.search).toEqual({ reason: "module_not_enabled", module: "contacts" });
  });

  it("redirects to the missing-permission /403 when the resolved view requires a permission the user lacks", async () => {
    const gatedView: ResolvedView = { ...LIST_VIEW, permissions: ["contacts:contact:read"] };
    viewRegistryRef.current = registryResolvingTo("/contacts", gatedView);
    permissionDataRef.current = {
      permissions: new Set(), // module enabled but the specific permission is missing
      fieldAccess: {},
      modulesEnabled: new Set(["contacts"]),
    };

    const router = await renderAt("/_m/contacts");

    expect(await screen.findByRole("heading", { name: "You don't have access to this page" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/403");
    expect(router.state.location.search).toEqual({ reason: "missing_permission" });
  });

  it("does not redirect when the module is enabled and every required permission is held", async () => {
    const gatedView: ResolvedView = { ...LIST_VIEW, permissions: ["contacts:contact:read"] };
    viewRegistryRef.current = registryResolvingTo("/contacts", gatedView);
    permissionDataRef.current = {
      permissions: new Set(["contacts:contact:read"]),
      fieldAccess: {},
      modulesEnabled: new Set(["contacts"]),
    };

    const router = await renderAt("/_m/contacts");

    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/_m/contacts");
    });
    expect(screen.queryByText("You don't have access to this page")).toBeNull();
    expect(screen.queryByText("Page not found")).toBeNull();
  });
});
