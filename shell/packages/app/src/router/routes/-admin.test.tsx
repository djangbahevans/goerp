import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const FAKE_TENANT = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

function fakeAuth(roles: string[]): AuthContextValue {
  const user: CurrentUser = {
    id: "u1",
    email: "a@b.com",
    contactId: null,
    name: null,
    avatarUrl: null,
    roles,
    amr: [],
    mfaVerifiedAt: null,
    mfaSetupRequired: false,
  };
  return {
    state: { status: "authenticated", user, tenant: FAKE_TENANT },
    isAuthenticated: true,
    user,
    tenant: FAKE_TENANT,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
}

async function renderAt(path: string, auth: AuthContextValue) {
  const router = createRouter({
    routeTree,
    context: { auth },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

afterEach(cleanup);

describe("/admin", () => {
  it("redirects an admin from /admin to /admin/users inside the admin rail", async () => {
    const router = await renderAt("/admin", fakeAuth(["admin"]));

    expect(router.state.location.pathname).toBe("/admin/users");
    expect(await screen.findByRole("navigation", { name: "Administration" })).toBeTruthy();
  });

  it("marks the rail entry for the current admin page as active", async () => {
    await renderAt("/admin/users", fakeAuth(["admin"]));

    const nav = await screen.findByRole("navigation", { name: "Administration" });
    const users = nav.querySelector('a[href="/admin/users"]');
    expect(users?.getAttribute("aria-current")).toBe("page");
  });

  it.each(["/admin", "/admin/users", "/admin/roles/r1"])("redirects a non-admin from %s to /403", async (path) => {
    const router = await renderAt(path, fakeAuth(["member"]));

    expect(router.state.location.pathname).toBe("/403");
    expect(router.state.location.search).toEqual({ reason: "missing_permission" });
    expect(screen.queryByRole("navigation", { name: "Administration" })).toBeNull();
  });

  it("leaves an unsettled session in place instead of sending it to /403", async () => {
    const auth: AuthContextValue = {
      ...fakeAuth([]),
      state: { status: "checking" },
      isAuthenticated: false,
      user: null,
    };
    const router = createRouter({
      routeTree,
      context: { auth },
      history: createMemoryHistory({ initialEntries: ["/admin/users"] }),
    });
    await router.load();

    expect(router.state.location.pathname).toBe("/admin/users");
  });
});
