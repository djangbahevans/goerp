import type { AuthContextValue, PermissionData } from "@goerp/sdk/auth";
import {
  AuthContext,
  createPermissionContextValue,
  PermissionContext,
  type PermissionsStatus,
  PermissionsStatusContext,
} from "@goerp/sdk/auth";
import {
  buildEmptyViewRegistry,
  type LoadStatus,
  type NavigationGroup,
  type ViewRegistry,
  ViewRegistryContext,
  ViewRegistryStatusContext,
} from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
const FAKE_AUTH: AuthContextValue = {
  state: { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT },
  isAuthenticated: true,
  user: FAKE_USER,
  tenant: FAKE_TENANT,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

function group(module: string, children: NavigationGroup["children"], permission?: string): NavigationGroup {
  return { key: module, label: module, icon: "folder", module, children, ...(permission ? { permission } : {}) };
}

const SALES = group("sales", [
  { key: "sales.help", label: "Help", path: "https://help.example.com", icon: "help-circle", external: true },
  { key: "sales.orders", label: "Orders", path: "/_m/sales/orders", icon: "receipt" },
]);
const HR = group("hr", [{ key: "hr.employees", label: "Employees", path: "/_m/hr/employees", icon: "users" }]);

function registryWith(tree: NavigationGroup[]): ViewRegistry {
  return { ...buildEmptyViewRegistry(), navigationTree: tree };
}

function permissions(modules: string[], granted: string[] = []): PermissionData {
  return { permissions: new Set(granted), fieldAccess: {}, modulesEnabled: new Set(modules) };
}

async function renderAt(
  path: string,
  {
    tree = [SALES, HR],
    perms = permissions(["sales", "hr"]),
    registryStatus = "ready",
    permissionsStatus = "ready",
  }: {
    tree?: NavigationGroup[];
    perms?: PermissionData;
    registryStatus?: LoadStatus;
    permissionsStatus?: PermissionsStatus;
  } = {},
) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createRouter({ routeTree, context: { auth: FAKE_AUTH }, history });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={FAKE_AUTH}>
        <PermissionContext.Provider value={createPermissionContextValue(perms)}>
          <PermissionsStatusContext.Provider value={permissionsStatus}>
            <ViewRegistryContext.Provider value={registryWith(tree)}>
              <ViewRegistryStatusContext.Provider value={registryStatus}>
                <AuthRouterProvider router={router} />
              </ViewRegistryStatusContext.Provider>
            </ViewRegistryContext.Provider>
          </PermissionsStatusContext.Provider>
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return { router, history };
}

afterEach(cleanup);

describe("/", () => {
  it("redirects to the first accessible in-app nav item, replacing / in history", async () => {
    const { router, history } = await renderAt("/");

    await waitFor(() => expect(router.state.location.pathname).toBe("/_m/sales/orders"));
    expect(history.length).toBe(1);
  });

  it("skips groups the user can't access", async () => {
    const { router } = await renderAt("/", { perms: permissions(["hr"]) });
    await waitFor(() => expect(router.state.location.pathname).toBe("/_m/hr/employees"));
  });

  it("skips items hidden by a permission check", async () => {
    const guarded = group("sales", [
      {
        key: "sales.orders",
        label: "Orders",
        path: "/_m/sales/orders",
        icon: "receipt",
        permission: "sales:order:read",
      },
    ]);
    const { router } = await renderAt("/", { tree: [guarded, HR] });
    await waitFor(() => expect(router.state.location.pathname).toBe("/_m/hr/employees"));
  });

  it("shows an empty state when nothing is accessible", async () => {
    await renderAt("/", { perms: permissions([]) });
    expect(await screen.findByText("No modules are available to you yet")).toBeTruthy();
  });

  it.each([
    ["the view registry", { registryStatus: "loading" as const }],
    ["permissions", { permissionsStatus: "loading" as const }],
  ])("shows neither the empty state nor a redirect while %s is still loading", async (_label, loading) => {
    const { router } = await renderAt("/", { perms: permissions([]), ...loading });

    await waitFor(() => expect(router.state.status).toBe("idle"));
    expect(screen.queryByText("No modules are available to you yet")).toBeNull();
    expect(router.state.location.pathname).toBe("/");
  });
});

describe("/ after a failed load", () => {
  it.each([
    ["the view registry", { registryStatus: "error" as const }],
    ["permissions", { permissionsStatus: "error" as const }],
  ])("reports a load failure of %s instead of claiming no access", async (_label, failure) => {
    await renderAt("/", { perms: permissions([]), ...failure });

    expect(await screen.findByText("Couldn't load your workspace")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
    expect(screen.queryByText("No modules are available to you yet")).toBeNull();
  });
});

describe("/settings", () => {
  it("redirects to /settings/profile", async () => {
    const { router } = await renderAt("/settings");
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(await screen.findByRole("heading", { name: "Profile" })).toBeTruthy();
  });
});
