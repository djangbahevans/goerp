import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RootLayout } from "./root-layout.js";

vi.mock("../chrome/chrome-header.js", () => ({ ChromeHeader: () => <header /> }));
vi.mock("../chrome/chrome-sidebar.js", () => ({ ChromeSidebar: () => <nav aria-label="Main" /> }));
vi.mock("../chrome/command-palette.js", () => ({ CommandPalette: () => null }));

afterEach(cleanup);

function deferred() {
  let resolve: () => void = () => {};
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// /app and /auth/sign-in each wait on a loader the test resolves by hand, so
// a navigation between them can be observed while it's still pending.
async function renderAt(path: string) {
  const loaders = { app: deferred(), auth: deferred() };
  let pending = false;
  const rootRoute = createRootRoute({ component: RootLayout });
  const app = createRoute({
    getParentRoute: () => rootRoute,
    path: "/app",
    loader: () => (pending ? loaders.app.promise : undefined),
    component: () => <p>app page</p>,
  });
  const auth = createRoute({
    getParentRoute: () => rootRoute,
    path: "/auth/sign-in",
    loader: () => (pending ? loaders.auth.promise : undefined),
    component: () => <p>sign-in page</p>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([app, auth]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PermissionContext.Provider value={permissions}>
        <RouterProvider router={router} />
      </PermissionContext.Provider>
    </QueryClientProvider>,
  );
  pending = true;
  return { router, loaders };
}

const hasChrome = () => screen.queryByRole("navigation", { name: "Main" }) !== null;

describe("RootLayout", () => {
  it("keeps the chrome around an app page until navigation to an auth page resolves", async () => {
    const { router, loaders } = await renderAt("/app");
    expect(hasChrome()).toBe(true);

    let navigation: Promise<void> = Promise.resolve();
    act(() => {
      navigation = router.navigate({ href: "/auth/sign-in" });
    });
    await waitFor(() => expect(router.state.status).toBe("pending"));
    expect(hasChrome()).toBe(true);
    expect(screen.getByText("app page")).toBeTruthy();

    await act(async () => {
      loaders.auth.resolve();
      await navigation;
    });
    expect(hasChrome()).toBe(false);
    expect(screen.getByText("sign-in page")).toBeTruthy();
  });

  it("keeps an auth page bare until navigation to an app page resolves", async () => {
    const { router, loaders } = await renderAt("/auth/sign-in");
    expect(hasChrome()).toBe(false);

    let navigation: Promise<void> = Promise.resolve();
    act(() => {
      navigation = router.navigate({ href: "/app" });
    });
    await waitFor(() => expect(router.state.status).toBe("pending"));
    expect(hasChrome()).toBe(false);
    expect(screen.getByText("sign-in page")).toBeTruthy();

    await act(async () => {
      loaders.app.resolve();
      await navigation;
    });
    expect(hasChrome()).toBe(true);
    expect(screen.getByRole("main").textContent).toContain("app page");
  });
});
