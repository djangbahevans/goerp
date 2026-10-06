import type { AuthContextValue } from "@goerp/sdk/auth";
import {
  AuthContext,
  createPermissionContextValue,
  PermissionContext,
  PermissionsStatusContext,
  permissionDataRef,
} from "@goerp/sdk/auth";
import type { ResolvedView, ViewRegistry } from "@goerp/sdk/schema";
import {
  buildEmptyViewRegistry,
  componentRegistry,
  ViewRegistryContext,
  ViewRegistryStatusContext,
  viewRegistryRef,
} from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";
import type { RouterContext } from "./__root.js";

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
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const FAKE_TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  firstDayOfWeek: "monday" as const,
  numberFormat: "1,234.56" as const,
  passwordMinLength: 12,
};
const FAKE_AUTH: AuthContextValue = {
  state: { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT },
  isAuthenticated: true,
  user: FAKE_USER,
  tenant: FAKE_TENANT,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
  expireSession: () => {},
};

type Workspace = NonNullable<RouterContext["workspace"]>;

async function renderAt(path: string, initial: Partial<Workspace> = {}) {
  let workspace: Workspace = {
    registry: viewRegistryRef.current,
    permissions: createPermissionContextValue(permissionDataRef.current),
    registryStatus: "ready",
    permissionsStatus: "ready",
    ...initial,
  };
  const router = createRouter({
    routeTree,
    context: { auth: FAKE_AUTH, workspace },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  const queryClient = new QueryClient();
  const tree = (snapshot: Workspace) => (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={FAKE_AUTH}>
        <PermissionContext.Provider value={snapshot.permissions}>
          <PermissionsStatusContext.Provider value={snapshot.permissionsStatus}>
            <ViewRegistryContext.Provider value={snapshot.registry}>
              <ViewRegistryStatusContext.Provider value={snapshot.registryStatus}>
                <AuthRouterProvider router={router} />
              </ViewRegistryStatusContext.Provider>
            </ViewRegistryContext.Provider>
          </PermissionsStatusContext.Provider>
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
  const rendered = render(tree(workspace));
  return {
    router,
    updateWorkspace: (next: Partial<Workspace>) => {
      workspace = { ...workspace, ...next };
      rendered.rerender(tree(workspace));
    },
  };
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
  componentRegistry.unregister("RouteProbe");
});

describe("/_m/$ catch-all route", () => {
  it("renders the not-found page for a path nothing resolves", async () => {
    viewRegistryRef.current = buildEmptyViewRegistry();
    const { router } = await renderAt("/_m/nonexistent");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/nonexistent");
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  it("redirects to the module-not-enabled /403 when the resolved view's module isn't enabled", async () => {
    viewRegistryRef.current = registryResolvingTo("/contacts", LIST_VIEW);
    permissionDataRef.current = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };

    const { router } = await renderAt("/_m/contacts");

    expect(await screen.findByRole("heading", { name: "This module isn't enabled for your account" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/403");
    expect(router.state.location.search).toEqual({ reason: "module_not_enabled", module: "contacts" });
  });

  it("redirects to the missing-permission /403 when the resolved view requires a permission the user lacks", async () => {
    const gatedView: ResolvedView = { ...LIST_VIEW, permissions: ["contacts:contact:read"] };
    viewRegistryRef.current = registryResolvingTo("/contacts", gatedView);
    permissionDataRef.current = {
      permissions: new Set(),
      fieldAccess: {},
      modulesEnabled: new Set(["contacts"]),
    };

    const { router } = await renderAt("/_m/contacts");

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

    const { router } = await renderAt("/_m/contacts");

    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/_m/contacts");
    });
    expect(screen.queryByText("You don't have access to this page")).toBeNull();
    expect(screen.queryByText("Page not found")).toBeNull();
  });
});

const PROBE_VIEW: ResolvedView = {
  ...LIST_VIEW,
  viewType: "custom",
  declaration: {
    name: "contacts_probe",
    type: "custom",
    resource: "contacts.contact",
    label: "Contacts",
    component: "RouteProbe",
  },
};

function readyWorkspace(view = PROBE_VIEW): Partial<Workspace> {
  componentRegistry.register("RouteProbe", () => <h1>Loaded contacts</h1>);
  return {
    registry: registryResolvingTo("/contacts", view),
    permissions: createPermissionContextValue({
      permissions: new Set(["contacts:contact:read"]),
      fieldAccess: {},
      modulesEnabled: new Set(["contacts"]),
    }),
    registryStatus: "ready",
    permissionsStatus: "ready",
  };
}

describe("cold module view loads", () => {
  it("waits for the registry before resolving a valid path", async () => {
    const ready = readyWorkspace();
    const { router, updateWorkspace } = await renderAt("/_m/contacts", {
      ...ready,
      registry: buildEmptyViewRegistry(),
      registryStatus: "loading",
    });
    expect((await screen.findByRole("status")).textContent).toContain("Loading your workspace");
    expect(screen.queryByText("Page not found")).toBeNull();
    expect(router.state.location.pathname).toBe("/_m/contacts");

    updateWorkspace(ready);
    expect(await screen.findByRole("heading", { name: "Loaded contacts" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/contacts");
  });

  it("does not declare an unknown path missing until the registry is ready", async () => {
    const { router, updateWorkspace } = await renderAt("/_m/nonexistent", { registryStatus: "loading" });
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.queryByText("Page not found")).toBeNull();

    updateWorkspace({ registryStatus: "ready" });
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/nonexistent");
  });

  it("waits for permissions that arrive after the registry, without an early 403", async () => {
    const ready = readyWorkspace({ ...PROBE_VIEW, permissions: ["contacts:contact:read"] });
    const { router, updateWorkspace } = await renderAt("/_m/contacts", {
      ...ready,
      permissions: createPermissionContextValue(permissionDataRef.current),
      permissionsStatus: "loading",
    });
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/contacts");
    expect(screen.queryByText("This module isn't enabled for your account")).toBeNull();

    updateWorkspace(ready);
    expect(await screen.findByRole("heading", { name: "Loaded contacts" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/_m/contacts");
  });

  it.each(["registryStatus", "permissionsStatus"] as const)(
    "reports a %s failure while the other request is still pending",
    async (status) => {
      const { router, updateWorkspace } = await renderAt("/_m/contacts", {
        registryStatus: "loading",
        permissionsStatus: "loading",
      });
      expect(await screen.findByRole("status")).toBeTruthy();
      updateWorkspace({ [status]: "error" });
      expect(await screen.findByText("Couldn't load your workspace")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
      expect(screen.queryByText("Page not found")).toBeNull();
      expect(router.state.location.pathname).toBe("/_m/contacts");
    },
  );

  it("uses current provider snapshots even when the SDK's imperative refs are still empty", async () => {
    await renderAt("/_m/contacts", readyWorkspace());
    expect(await screen.findByRole("heading", { name: "Loaded contacts" })).toBeTruthy();
    expect(viewRegistryRef.current.resolveRoute("/contacts")).toBeNull();
    expect(permissionDataRef.current.modulesEnabled.size).toBe(0);
  });

  it("rechecks access when loaded permissions change", async () => {
    const ready = readyWorkspace({ ...PROBE_VIEW, permissions: ["contacts:contact:read"] });
    const { router, updateWorkspace } = await renderAt("/_m/contacts", ready);
    await screen.findByRole("heading", { name: "Loaded contacts" });

    updateWorkspace({
      permissions: createPermissionContextValue({
        permissions: new Set(),
        fieldAccess: {},
        modulesEnabled: new Set(["contacts"]),
      }),
    });
    expect(await screen.findByRole("heading", { name: "You don't have access to this page" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/403");
    expect(router.state.location.search).toEqual({ reason: "missing_permission" });
  });
});
