import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { Toast } from "@goerp/sdk/components";
import { ModuleNavigationProvider, useModule } from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useModuleNavigate } from "./use-module-navigate.js";

const ME: CurrentUser = {
  id: "me",
  email: "ada@acme.test",
  contactId: null,
  name: "Ada",
  avatarUrl: null,
  roles: ["user"],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
};
const TENANT = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };
const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: ME, tenant: TENANT },
  isAuthenticated: true,
  user: ME,
  tenant: TENANT,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

// A module view, as a module author would write it.
function ContactsHome() {
  const { navigate, toast } = useModule("contacts");
  return (
    <>
      <button type="button" onClick={() => navigate("/contacts?stage=lead")}>
        Leads
      </button>
      <button type="button" onClick={() => navigate("/contacts", { replace: true })}>
        Replace
      </button>
      <button type="button" onClick={() => toast.success("Contact saved")}>
        Save
      </button>
    </>
  );
}

// The shell's side: RootLayout's provider, over a real router.
function renderShell() {
  const rootRoute = createRootRoute({
    component: () => (
      <ModuleNavigationProvider navigate={useModuleNavigate()}>
        <Outlet />
        <Toast />
      </ModuleNavigationProvider>
    ),
  });
  const routes = [
    createRoute({ getParentRoute: () => rootRoute, path: "/", component: ContactsHome }),
    createRoute({ getParentRoute: () => rootRoute, path: "/contacts", component: () => <p>Contacts list</p> }),
  ];
  const history = createMemoryHistory({ initialEntries: ["/"] });
  const router = createRouter({ routeTree: rootRoute.addChildren(routes), history });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={AUTH}>
        <PermissionContext.Provider
          value={createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() })}
        >
          <RouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return { router, history };
}

afterEach(cleanup);

describe("useModule inside the shell", () => {
  it("navigates to a path with its query string", async () => {
    const { router } = renderShell();
    fireEvent.click(await screen.findByRole("button", { name: "Leads" }));
    expect(await screen.findByText("Contacts list")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/contacts");
    expect(router.state.location.search).toEqual({ stage: "lead" });
  });

  it("replaces the current history entry when asked", async () => {
    const { router, history } = renderShell();
    fireEvent.click(await screen.findByRole("button", { name: "Replace" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts"));
    act(() => history.back());
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts"));
    expect(history.length).toBe(1);
  });

  it("shows a toast through the shell's toast stack", async () => {
    renderShell();
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(await screen.findByText("Contact saved")).toBeTruthy();
  });
});
