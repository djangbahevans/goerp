import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CatalogPermission } from "../../admin/roles/admin-roles-api.js";
import {
  type FakeRole,
  type FakeRolesBackend,
  type FakeRolesBackendOptions,
  installFakeAdminRolesBackend,
} from "../../admin/roles/fake-admin-roles-backend.js";
import {
  type FakeBackend,
  type FakeUser,
  installFakeAdminUsersBackend,
} from "../../admin/users/fake-admin-users-backend.js";
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

const CATALOG: CatalogPermission[] = [
  { name: "contacts:contact:merge", description: "Merge duplicate contacts", category: "Contacts", module: "contacts" },
  { name: "contacts:contact:read", description: "View contacts", category: "Contacts", module: "contacts" },
  { name: "contacts:contact:write", description: "Create and edit contacts", category: "Contacts", module: "contacts" },
  { name: "sales:order:confirm", description: "Confirm orders", category: "Sales", module: "sales" },
  { name: "sales:order:read", description: "View orders", category: "Sales", module: "sales" },
];

const ROLES: FakeRole[] = [
  {
    id: "r-admin",
    name: "admin",
    description: "All permissions",
    isImmutable: true,
    userCount: 1,
    permissions: CATALOG.map((p) => p.name),
  },
  { id: "r-user", name: "user", description: null, isImmutable: true, userCount: 2, permissions: ["sales:order:read"] },
  { id: "r-portal", name: "portal", description: null, isImmutable: true, userCount: 0, permissions: [] },
  {
    id: "r-clerk",
    name: "clerk",
    description: "Front desk",
    isImmutable: false,
    userCount: 1,
    permissions: ["contacts:contact:read", "legacy:thing:read"],
  },
  {
    id: "r-offered",
    name: "offered",
    description: null,
    isImmutable: false,
    userCount: 0,
    invitationCount: 1,
    permissions: [],
  },
];

const USERS: FakeUser[] = [
  { id: "me", name: "Ada Admin", email: "ada@acme.test", roles: ["admin"], status: "active", lastLoginAt: null },
  {
    id: "u-bola",
    name: "Bola",
    email: "bola@acme.test",
    roles: ["clerk", "user"],
    status: "active",
    lastLoginAt: null,
  },
  { id: "u-chidi", name: "Chidi", email: "chidi@acme.test", roles: ["user"], status: "active", lastLoginAt: null },
];

let rolesBackend: FakeRolesBackend | null = null;
let usersBackend: FakeBackend | null = null;

async function renderAt(path: string, options: Partial<FakeRolesBackendOptions> = {}) {
  usersBackend = installFakeAdminUsersBackend({ users: USERS });
  rolesBackend = installFakeAdminRolesBackend({ roles: ROLES, catalog: CATALOG, ...options });
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
  rolesBackend?.restore();
  usersBackend?.restore();
  rolesBackend = null;
  usersBackend = null;
});

function checkbox(name: string): HTMLInputElement {
  return screen.getByRole("checkbox", { name }) as HTMLInputElement;
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement;
}

function lastRequest(method: string) {
  return rolesBackend?.requests.filter((request) => request.method === method).at(-1);
}

describe("/admin/roles", () => {
  it("lists built-in roles first with a System badge and user counts, under the Roles rail entry", async () => {
    await renderAt("/admin/roles");
    const tbl = await screen.findByRole("table");
    const rows = within(tbl).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getAllByRole("cell")[0]?.textContent)).toEqual([
      "AdminSystem",
      "PortalSystem",
      "UserSystem",
      "clerk",
      "offered",
    ]);
    const clerk = rows[3] as HTMLElement;
    expect(within(clerk).getByText("Front desk")).toBeTruthy();
    expect(within(clerk).getByText("1 user")).toBeTruthy();
    expect(within(rows[2] as HTMLElement).getByText("2 users")).toBeTruthy();

    const rail = screen.getByRole("navigation", { name: "Administration" });
    expect(within(rail).getByRole("link", { name: "Roles" }).getAttribute("aria-current")).toBe("page");
  });

  it("opens a role from its row and the create form from Create role", async () => {
    const router = await renderAt("/admin/roles");
    fireEvent.click(within(await screen.findByRole("table")).getByText("clerk"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles/r-clerk"));
    expect(await screen.findByRole("heading", { name: "clerk" })).toBeTruthy();

    await router.navigate({ to: "/admin/roles" });
    fireEvent.click(await screen.findByRole("button", { name: "Create role" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles/new"));
    expect(await screen.findByRole("heading", { name: "New role" })).toBeTruthy();
  });

  it("shows a retryable error when the list fails to load", async () => {
    await renderAt("/admin/roles", { failAll: true });
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Couldn't load roles.")).toBeTruthy();
  });
});

describe("creating, editing and deleting a role", () => {
  it("creates a role through the matrix, renames it, and deletes it", async () => {
    const router = await renderAt("/admin/roles/new");
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "sales_rep" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), { target: { value: "Sells things" } });
    fireEvent.click(checkbox("sales:order:confirm"));
    expect(checkbox("sales:order:read").checked).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Create role" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles/role-1000"));
    expect(lastRequest("POST")?.body).toEqual({
      name: "sales_rep",
      description: "Sells things",
      permissions: ["sales:order:confirm", "sales:order:read"],
    });
    expect(await screen.findByRole("heading", { name: "sales_rep" })).toBeTruthy();
    expect(checkbox("sales:order:confirm").checked).toBe(true);
    expect(saveButton().disabled).toBe(true);

    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "account_rep" } });
    expect(saveButton().disabled).toBe(false);
    fireEvent.click(saveButton());
    expect(await screen.findByRole("heading", { name: "account_rep" })).toBeTruthy();
    expect(lastRequest("PATCH")?.body).toEqual({ name: "account_rep" });
    expect(saveButton().disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Delete role" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete role" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles"));
    expect(rolesBackend?.roles().some((role) => role.name === "account_rep")).toBe(false);
  });

  it("rejects an invalid name before sending, and shows a taken name on the field", async () => {
    await renderAt("/admin/roles/new");
    const name = await screen.findByRole("textbox", { name: "Name" });
    fireEvent.change(name, { target: { value: "Sales Rep" } });
    fireEvent.click(screen.getByRole("button", { name: "Create role" }));
    expect(await screen.findByText(/lowercase letters, digits or underscores, starting/)).toBeTruthy();
    expect(lastRequest("POST")).toBeUndefined();

    fireEvent.change(name, { target: { value: "clerk" } });
    fireEvent.click(screen.getByRole("button", { name: "Create role" }));
    expect(await screen.findByText("Another role already has this name.")).toBeTruthy();
  });

  it("applies the read dependency rules in both directions", async () => {
    await renderAt("/admin/roles/new");
    await screen.findByRole("textbox", { name: "Name" });

    fireEvent.click(checkbox("contacts:contact:write"));
    expect(checkbox("contacts:contact:read").checked).toBe(true);
    fireEvent.click(checkbox("contacts:contact:merge"));
    expect(checkbox("sales:order:read").checked).toBe(false);

    fireEvent.click(checkbox("contacts:contact:read"));
    expect(checkbox("contacts:contact:read").checked).toBe(false);
    expect(checkbox("contacts:contact:write").checked).toBe(false);
    expect(checkbox("contacts:contact:merge").checked).toBe(false);
  });

  it("enables Save changes only while something differs from the saved role", async () => {
    await renderAt("/admin/roles/r-clerk");
    await screen.findByRole("heading", { name: "clerk" });
    expect(saveButton().disabled).toBe(true);

    fireEvent.click(checkbox("contacts:contact:write"));
    expect(saveButton().disabled).toBe(false);
    fireEvent.click(checkbox("contacts:contact:write"));
    expect(saveButton().disabled).toBe(true);

    fireEvent.click(checkbox("contacts:contact:write"));
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(checkbox("contacts:contact:write").checked).toBe(false);
    expect(saveButton().disabled).toBe(true);
  });

  it("keeps a held permission the catalog no longer lists, and sends it back on save", async () => {
    await renderAt("/admin/roles/r-clerk");
    await screen.findByRole("heading", { name: "clerk" });
    expect(screen.getByRole("group", { name: "Other permissions" })).toBeTruthy();
    expect(checkbox("legacy:thing:read").checked).toBe(true);

    fireEvent.click(checkbox("contacts:contact:write"));
    fireEvent.click(saveButton());
    await waitFor(() => expect(saveButton().disabled).toBe(true));
    expect(lastRequest("PATCH")?.body).toEqual({
      permissions: ["contacts:contact:read", "contacts:contact:write", "legacy:thing:read"],
    });
  });

  it("disables Delete role while users hold the role or an invitation offers it", async () => {
    const router = await renderAt("/admin/roles/r-clerk");
    await screen.findByRole("heading", { name: "clerk" });
    expect((screen.getByRole("button", { name: "Delete role" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Remove it from its 1 user before deleting it.")).toBeTruthy();

    await router.navigate({ to: "/admin/roles/$roleId", params: { roleId: "r-offered" } });
    await screen.findByRole("heading", { name: "offered" });
    expect((screen.getByRole("button", { name: "Delete role" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/A pending invitation offers this role/)).toBeTruthy();
  });

  it("links the user count to the users list filtered by the role", async () => {
    const router = await renderAt("/admin/roles/r-clerk");
    const link = await screen.findByRole("link", { name: "1 user" });
    expect(link.getAttribute("href")).toBe("/admin/users?role=clerk");
    fireEvent.click(link);

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/users"));
    expect(router.state.location.search).toEqual({ role: "clerk" });
    const tbl = await screen.findByRole("table");
    await waitFor(() => expect(within(tbl).getAllByRole("row")).toHaveLength(2));
    expect(within(tbl).getByText("bola@acme.test")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Show all roles" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    await waitFor(() => expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(4));
  });
});

describe("built-in roles", () => {
  it("render read-only and offer neither save nor delete", async () => {
    await renderAt("/admin/roles/r-user");
    expect(await screen.findByRole("heading", { name: "UserSystem" })).toBeTruthy();
    expect(screen.getByText(/is a built-in role/)).toBeTruthy();
    expect(checkbox("sales:order:read").checked).toBe(true);
    for (const box of screen.getAllByRole("checkbox")) expect((box as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete role" })).toBeNull();
  });
});

describe("assigning custom roles", () => {
  it("offers the tenant's roles, custom ones included, in a user's Add role dropdown", async () => {
    await renderAt("/admin/users/u-chidi");
    const trigger = await screen.findByRole("combobox", { name: "Add role" });
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    const listbox = await screen.findByRole("listbox");
    await waitFor(() => expect(within(listbox).getByRole("option", { name: "clerk" })).toBeTruthy());
    expect(within(listbox).queryByRole("option", { name: "User" })).toBeNull();
  });
});
