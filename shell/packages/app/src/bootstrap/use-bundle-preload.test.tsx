import type { AuthContextValue } from "@goerp/sdk/auth";
import {
  AuthContext,
  createPermissionContextValue,
  PermissionContext,
  PermissionsStatusContext,
} from "@goerp/sdk/auth";
import type { NavigationGroup, ResolvedView, ViewRegistry } from "@goerp/sdk/schema";
import { buildEmptyViewRegistry, ViewRegistryContext, ViewRegistryStatusContext } from "@goerp/sdk/schema";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readModuleHistory } from "./route-prediction.js";
import { useBundlePreload } from "./use-bundle-preload.js";

// A plain function rather than vi.fn: a spy observes the promises it returns, which would hide an
// unhandled rejection.
const loader = vi.hoisted(() => ({ calls: [] as unknown[][], fail: false }));
vi.mock("./module-loader.js", () => ({
  ensureModuleRegistered: (...args: unknown[]) => {
    loader.calls.push(args);
    return loader.fail ? Promise.reject(new Error("bundle fetch failed")) : Promise.resolve();
  },
}));

const USER_ID = "u1";
const AUTH = { user: { id: USER_ID }, isAuthenticated: true } as unknown as AuthContextValue;

function view(module: string): ResolvedView {
  return {
    module,
    viewName: `${module}_list`,
    viewType: "list",
    declaration: { name: `${module}_list`, type: "list", resource: `${module}.record`, label: module },
    permissions: [],
    bundleUrl: `/bundles/${module}.js`,
  };
}

const MODULES = ["crm", "sales", "stock", "hr"];
const VIEWS = new Map(MODULES.map((module) => [`/${module}/home`, view(module)]));
const NAVIGATION: NavigationGroup[] = MODULES.map((module) => ({
  key: module,
  label: module,
  icon: "folder",
  module,
  children: [{ key: module, label: module, path: `/${module}/home`, icon: "circle" }],
}));
const REGISTRY: ViewRegistry = {
  ...buildEmptyViewRegistry(),
  navigationTree: NAVIGATION,
  resolveRoute: (path) => VIEWS.get(path) ?? null,
  getBundleUrl: (module) => `/bundles/${module}.js`,
  getBundleSHA256: (module) => `sha256:${module}`,
};
const PERMISSIONS = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(MODULES),
});

function renderAt(path: string, enabled = true) {
  const rootRoute = createRootRoute({
    component: function Root() {
      useBundlePreload(enabled);
      return <Outlet />;
    },
  });
  const moduleRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/_m/$",
    component: () => <h1>Module page</h1>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([moduleRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <AuthContext.Provider value={AUTH}>
      <PermissionContext.Provider value={PERMISSIONS}>
        <PermissionsStatusContext.Provider value="ready">
          <ViewRegistryContext.Provider value={REGISTRY}>
            <ViewRegistryStatusContext.Provider value="ready">
              <RouterProvider router={router} />
            </ViewRegistryStatusContext.Provider>
          </ViewRegistryContext.Provider>
        </PermissionsStatusContext.Provider>
      </PermissionContext.Provider>
    </AuthContext.Provider>,
  );
  return router;
}

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  loader.calls = [];
  loader.fail = false;
});

describe("useBundlePreload", () => {
  it("loads the next three predicted modules' bundles after the first route renders", async () => {
    renderAt("/_m/crm/home");
    await screen.findByRole("heading", { name: "Module page" });

    await waitFor(() => expect(loader.calls).toHaveLength(3));
    expect(loader.calls).toEqual([
      ["sales", "/bundles/sales.js", "sha256:sales"],
      ["stock", "/bundles/stock.js", "sha256:stock"],
      ["hr", "/bundles/hr.js", "sha256:hr"],
    ]);
  });

  it("records the visited module and ranks earlier visits first", async () => {
    window.localStorage.setItem(`goerp-module-history:${USER_ID}`, JSON.stringify(["hr"]));
    renderAt("/_m/sales/home");

    await waitFor(() => expect(loader.calls).toHaveLength(3));
    expect(loader.calls.map(([module]) => module)).toEqual(["hr", "crm", "stock"]);
    expect(readModuleHistory(USER_ID)).toEqual(["sales", "hr"]);
  });

  it("drops a failed preload silently", async () => {
    const unhandled = vi.fn();
    process.on("unhandledRejection", unhandled);
    loader.fail = true;
    try {
      renderAt("/_m/crm/home");
      await waitFor(() => expect(loader.calls).toHaveLength(3));
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(unhandled).not.toHaveBeenCalled();
      expect(screen.getByRole("heading", { name: "Module page" })).toBeTruthy();
    } finally {
      process.off("unhandledRejection", unhandled);
    }
  });

  it("preloads once per session, not on every navigation", async () => {
    const router = renderAt("/_m/crm/home");
    await waitFor(() => expect(loader.calls).toHaveLength(3));

    await act(() => router.navigate({ to: "/_m/$", params: { _splat: "hr/home" } }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(loader.calls).toHaveLength(3);
    expect(readModuleHistory(USER_ID)).toEqual(["hr", "crm"]);
  });

  it("does nothing while disabled", async () => {
    renderAt("/_m/crm/home", false);
    await screen.findByRole("heading", { name: "Module page" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(loader.calls).toEqual([]);
    expect(readModuleHistory(USER_ID)).toEqual([]);
  });
});
