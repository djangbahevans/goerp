import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
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
}

afterEach(cleanup);

describe("root layout", () => {
  it("renders an app route inside the chrome", async () => {
    renderAt("/403", SIGNED_IN);

    const main = await screen.findByRole("main");
    expect(main.textContent).toContain("You don't have permission to view this page");
    expect(screen.getByRole("navigation", { name: "Main" })).toBeTruthy();
    expect(screen.getByRole("banner")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Skip to content" })).toBeTruthy();
  });

  it("renders an /auth/* route with no sidebar or header", async () => {
    renderAt("/auth/login", SIGNED_OUT);

    expect(await screen.findByRole("button", { name: /sign in/i })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
    expect(screen.queryByRole("banner")).toBeNull();
    expect(screen.queryByRole("link", { name: "Skip to content" })).toBeNull();
  });
});
