import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CONTACTS, HR, PAYROLL_PLUS, SALES } from "../../admin/modules/admin-modules-story-fixtures.js";
import {
  type FakeModulesBackend,
  type FakeModulesBackendOptions,
  installFakeAdminModulesBackend,
} from "../../admin/modules/fake-admin-modules-backend.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

// jsdom has no scrollIntoView, which Radix Select calls when its panel opens.
Element.prototype.scrollIntoView = vi.fn();

const ME: CurrentUser = {
  id: "me",
  email: "ada@acme.test",
  contactId: null,
  name: "Ada Admin",
  avatarUrl: null,
  roles: ["admin"],
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
const TENANT = {
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
const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: ME, tenant: TENANT },
  isAuthenticated: true,
  user: ME,
  tenant: TENANT,
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

let backend: FakeModulesBackend | null = null;

async function renderAt(path: string, options: Partial<FakeModulesBackendOptions> = {}) {
  backend = installFakeAdminModulesBackend({ modules: [CONTACTS, SALES, HR, PAYROLL_PLUS], ...options });
  const router = createRouter({
    routeTree,
    context: { auth: AUTH },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={AUTH}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

afterEach(() => {
  cleanup();
  backend?.restore();
  backend = null;
});

function patches() {
  return (backend?.requests ?? []).filter((r) => r.method === "PATCH");
}

function cardOf(headings: HTMLElement[], name: string): HTMLElement {
  return headings.find((h) => h.textContent === name)?.closest("li") as HTMLElement;
}

describe("/admin/modules", () => {
  it("lists every module with its state, under the Modules rail entry", async () => {
    await renderAt("/admin/modules");

    const headings = await screen.findAllByRole("heading", { level: 2 });
    expect(headings.map((h) => h.textContent)).toEqual(["Contacts", "HR", "Payroll Plus", "Sales"]);
    expect(within(cardOf(headings, "Contacts")).getByText("Active")).toBeTruthy();
    expect(within(cardOf(headings, "Contacts")).getByText("Version 0.1.0")).toBeTruthy();
    expect(within(cardOf(headings, "HR")).getByText("Disabled")).toBeTruthy();
    expect(within(cardOf(headings, "Payroll Plus")).getByText("Not on your plan")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Modules" }).getAttribute("aria-current")).toBe("page");
  });

  it("filters by Active and Disabled", async () => {
    await renderAt("/admin/modules");
    await screen.findAllByRole("heading", { level: 2 });

    fireEvent.click(screen.getByRole("tab", { name: "Active" }));
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Contacts", "Sales"]);

    fireEvent.click(screen.getByRole("tab", { name: "Disabled" }));
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["HR", "Payroll Plus"]);
  });

  it("opens a module from its View button", async () => {
    const router = await renderAt("/admin/modules");
    fireEvent.click(await screen.findByRole("button", { name: "View Sales" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/modules/sales"));
    expect(await screen.findByRole("heading", { name: "Sales", level: 1 })).toBeTruthy();
  });

  it("offers a retry when the list fails to load", async () => {
    await renderAt("/admin/modules", { failAll: true });
    expect(await screen.findByText("Couldn't load modules.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("says so when no module is installed", async () => {
    await renderAt("/admin/modules", { modules: [] });
    expect(await screen.findByText("No modules installed")).toBeTruthy();
  });
});

describe("/admin/modules/:name", () => {
  it("shows the module's dependencies and permissions", async () => {
    await renderAt("/admin/modules/sales");

    expect(await screen.findByRole("heading", { name: "Sales", level: 1 })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Contacts" })).toBeTruthy();
    expect(screen.getByText("sales:order:read")).toBeTruthy();
    expect(screen.getByText("View sales orders")).toBeTruthy();
  });

  it("disables a module only after confirmation, then enables it again", async () => {
    await renderAt("/admin/modules/sales");
    fireEvent.click(await screen.findByRole("button", { name: "Disable module" }));
    expect(patches()).toHaveLength(0);

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("This will hide all Sales data from users. Data is not deleted.")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Disable" }));

    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]).toMatchObject({ path: "/admin/modules/sales/settings", body: { enabled: false } });
    fireEvent.click(await screen.findByRole("button", { name: "Enable module" }));
    await waitFor(() => expect(patches()).toHaveLength(2));
    expect(patches()[1]?.body).toEqual({ enabled: true });
    expect(await screen.findByRole("button", { name: "Disable module" })).toBeTruthy();
  });

  it("leaves the module enabled when another module depends on it", async () => {
    await renderAt("/admin/modules/contacts");
    fireEvent.click(await screen.findByRole("button", { name: "Disable module" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Disable" }));

    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(await screen.findByRole("button", { name: "Disable module" })).toBeTruthy();
    expect(backend?.modules().find((m) => m.name === "contacts")?.disabled).toBeFalsy();
  });

  it("offers no control for a module the plan does not include", async () => {
    await renderAt("/admin/modules/payroll_plus");
    expect(await screen.findByText("Your plan doesn't include Payroll Plus.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Enable module" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Disable module" })).toBeNull();
  });

  it("says so when the module is not installed", async () => {
    const router = await renderAt("/admin/modules/nope");
    expect(await screen.findByText("Module not found")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to modules" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/modules"));
  });
});
