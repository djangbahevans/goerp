import { AuthContext, type AuthContextValue, tenantSuspension } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, type ViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ErrorLayout } from "./error-layout.js";
import { ForbiddenPage } from "./forbidden-page.js";
import { NotFoundPage } from "./not-found-page.js";
import { ServerErrorPage } from "./server-error-page.js";
import { TenantSuspendedPage } from "./tenant-suspended-page.js";

afterEach(() => {
  cleanup();
  tenantSuspension.set(false);
});

function authWithRoles(roles: string[], overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  const user = {
    id: "u1",
    email: "ada@example.com",
    contactId: null,
    name: "Ada",
    avatarUrl: null,
    roles,
    amr: ["pwd"],
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
    login: async () => null,
    completeHandoff: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
    ...overrides,
  };
}

// Renders `page` at /start, after /previous in history, with / and
// /auth/login routes to navigate to.
async function renderPage(
  page: ReactNode,
  {
    auth = authWithRoles([]),
    registry = buildEmptyViewRegistry(),
    queryClient = new QueryClient(),
  }: { auth?: AuthContextValue; registry?: ViewRegistry; queryClient?: QueryClient } = {},
) {
  const rootRoute = createRootRoute();
  const route = (path: string, component: () => ReactNode) =>
    createRoute({ getParentRoute: () => rootRoute, path, component });
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      route("/", () => <p>home page</p>),
      route("/previous", () => <p>previous page</p>),
      route("/start", () => page),
      route("/auth/login", () => <p>login page</p>),
    ]),
    history: createMemoryHistory({ initialEntries: ["/previous", "/start"] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <ViewRegistryContext.Provider value={registry}>
          <RouterProvider router={router} />
        </ViewRegistryContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

describe("ErrorLayout", () => {
  it("renders the heading, description, actions and footnote", () => {
    render(
      <ErrorLayout
        icon="compass"
        heading="Lost"
        description="Nothing here."
        actions={<button type="button">Leave</button>}
        footnote="Ref 1"
      />,
    );
    expect(screen.getByRole("main")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1, name: "Lost" })).toBeTruthy();
    expect(screen.getByText("Nothing here.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Leave" })).toBeTruthy();
    expect(screen.getByText("Ref 1")).toBeTruthy();
  });

  it("renders no footnote when none is given", () => {
    render(<ErrorLayout icon="compass" heading="Lost" description="Nothing here." actions={null} />);
    expect(screen.getByRole("main").querySelectorAll("p")).toHaveLength(1);
  });
});

describe("NotFoundPage", () => {
  it("shows the not-found copy with a home link", async () => {
    await renderPage(<NotFoundPage />);
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(screen.getByText("This page doesn't exist or has been moved.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Go home" }).getAttribute("href")).toBe("/");
  });

  it("goes home", async () => {
    const router = await renderPage(<NotFoundPage />);
    fireEvent.click(await screen.findByRole("link", { name: "Go home" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("goes back to the previous page", async () => {
    const router = await renderPage(<NotFoundPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Go back" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/previous"));
  });
});

describe("ForbiddenPage", () => {
  const registry: ViewRegistry = {
    ...buildEmptyViewRegistry(),
    getModuleDisplayName: (name) => (name === "sales" ? "Sales" : null),
  };

  it("shows the missing-permission variant with only a home action", async () => {
    await renderPage(<ForbiddenPage reason="missing_permission" />, { auth: authWithRoles(["admin"]) });
    expect(await screen.findByRole("heading", { name: "You don't have access to this page" })).toBeTruthy();
    expect(screen.getByText("Contact your administrator to request access.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Go home" }).getAttribute("href")).toBe("/");
    expect(screen.queryByRole("link", { name: "Go to settings" })).toBeNull();
  });

  it("names the module by its display name in the module-not-enabled variant", async () => {
    await renderPage(<ForbiddenPage reason="module_not_enabled" module="sales" />, { registry });
    expect(await screen.findByRole("heading", { name: "This module isn't enabled for your account" })).toBeTruthy();
    expect(screen.getByText("Sales needs to be enabled in your organisation's settings.")).toBeTruthy();
  });

  it("falls back to the module's name when the registry has no display name for it", async () => {
    await renderPage(<ForbiddenPage reason="module_not_enabled" module="hr" />, { registry });
    expect(await screen.findByText("hr needs to be enabled in your organisation's settings.")).toBeTruthy();
  });

  it("offers admins the module settings", async () => {
    await renderPage(<ForbiddenPage reason="module_not_enabled" module="sales" />, {
      auth: authWithRoles(["admin"]),
      registry,
    });
    expect((await screen.findByRole("link", { name: "Go to settings" })).getAttribute("href")).toBe("/admin/modules");
    expect(screen.getByRole("link", { name: "Go home" })).toBeTruthy();
  });

  it("hides the module settings from non-admins", async () => {
    await renderPage(<ForbiddenPage reason="module_not_enabled" module="sales" />, {
      auth: authWithRoles(["sales_rep"]),
      registry,
    });
    expect(await screen.findByRole("link", { name: "Go home" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Go to settings" })).toBeNull();
  });
});

describe("ServerErrorPage", () => {
  it("shows the error reference when there is a trace ID", async () => {
    await renderPage(<ServerErrorPage traceId="4bf92f3577b34da6" />);
    expect(await screen.findByRole("heading", { name: "Something went wrong on our end" })).toBeTruthy();
    expect(screen.getByText("We've been notified and are working on it. Please try again.")).toBeTruthy();
    expect(screen.getByText("Error ref: 4bf92f3577b34da6")).toBeTruthy();
  });

  it("omits the error reference without a trace ID", async () => {
    await renderPage(<ServerErrorPage traceId={null} />);
    expect(await screen.findByRole("heading", { name: "Something went wrong on our end" })).toBeTruthy();
    expect(screen.queryByText(/Error ref/)).toBeNull();
  });

  it("reloads on Try again and links home", async () => {
    const reload = vi.fn();
    await renderPage(<ServerErrorPage traceId={null} reload={reload} />);
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect(reload).toHaveBeenCalledOnce();
    expect(screen.getByRole("link", { name: "Go home" }).getAttribute("href")).toBe("/");
  });
});

describe("TenantSuspendedPage", () => {
  it("links to the configured support URL in a new tab", async () => {
    await renderPage(<TenantSuspendedPage supportUrl="https://support.example.com" />);
    expect(await screen.findByRole("heading", { name: "This organisation's account has been suspended" })).toBeTruthy();
    expect(screen.getByText("Please contact support to resolve this.")).toBeTruthy();
    const support = screen.getByRole("link", { name: "Contact support" });
    expect(support.getAttribute("href")).toBe("https://support.example.com");
    expect(support.getAttribute("target")).toBe("_blank");
  });

  it("hides Contact support when no support URL is configured", async () => {
    await renderPage(<TenantSuspendedPage />);
    expect(await screen.findByRole("button", { name: "Sign in with a different account" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Contact support" })).toBeNull();
  });

  it("clears the session, the suspension and the cached tenant lookup, then goes to the login page", async () => {
    const logout = vi.fn(async () => {});
    const queryClient = new QueryClient();
    queryClient.setQueryData(["auth", "tenant-context"], null);
    act(() => tenantSuspension.set(true));
    const router = await renderPage(<TenantSuspendedPage />, { auth: authWithRoles([], { logout }), queryClient });

    fireEvent.click(await screen.findByRole("button", { name: "Sign in with a different account" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(logout).toHaveBeenCalledOnce();
    expect(tenantSuspension.get()).toBe(false);
    expect(queryClient.getQueryState(["auth", "tenant-context"])).toBeUndefined();
  });
});
