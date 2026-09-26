import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type FakeBackend,
  type FakeBackendOptions,
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
  theme: "system" as const,
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
};
const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: ME, tenant: TENANT },
  isAuthenticated: true,
  user: ME,
  tenant: TENANT,
  login: async () => null,
  completeHandoff: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const IN_A_WEEK = new Date(Date.now() + 6 * 24 * 3600 * 1000).toISOString();
const HOUR_AGO = new Date(Date.now() - 3600 * 1000).toISOString();

const USERS: FakeUser[] = [
  { id: "me", name: "Ada Admin", email: "ada@acme.test", roles: ["admin"], status: "active", lastLoginAt: HOUR_AGO },
  {
    id: "u-bola",
    name: "Bola Active",
    email: "bola@acme.test",
    roles: ["portal", "user"],
    status: "active",
    lastLoginAt: HOUR_AGO,
    phone: "+233 20 000 0000",
  },
  {
    id: "u-chidi",
    name: "Chidi Suspended",
    email: "chidi@acme.test",
    roles: ["user"],
    status: "suspended",
    lastLoginAt: null,
  },
  {
    id: "u-efua",
    name: "Efua Invitee",
    email: "efua@acme.test",
    roles: [],
    status: "invited",
    lastLoginAt: null,
    invitation: { id: "inv-efua", role: "user", expiresAt: IN_A_WEEK, createdAt: HOUR_AGO },
  },
];

let backend: FakeBackend | null = null;

async function renderAt(path: string, options: Partial<FakeBackendOptions> = {}) {
  backend = installFakeAdminUsersBackend({
    users: USERS,
    sessions: {
      "u-bola": [
        {
          id: "fam-laptop",
          userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/128.0.0.0 Safari/537.36",
          ipAddress: "41.66.1.2",
          countryCode: "GH",
          signedInAt: HOUR_AGO,
          lastActiveAt: HOUR_AGO,
        },
        {
          id: "fam-phone",
          userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) Safari/604.1",
          ipAddress: null,
          countryCode: null,
          signedInAt: HOUR_AGO,
          lastActiveAt: HOUR_AGO,
        },
      ],
    },
    ...options,
  });
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

async function table() {
  return screen.findByRole("table");
}

function rowEmails(tbl: HTMLElement): string[] {
  return within(tbl)
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[1]?.textContent ?? "");
}

describe("/admin/users", () => {
  it("lists members and invitees with their roles and status, under the Users rail entry", async () => {
    await renderAt("/admin/users");
    const tbl = await table();
    expect(rowEmails(tbl)).toEqual(["ada@acme.test", "bola@acme.test", "chidi@acme.test", "efua@acme.test"]);
    const bola = within(tbl).getByText("bola@acme.test").closest("tr") as HTMLElement;
    expect(within(bola).getByText("Portal, User")).toBeTruthy();
    expect(within(bola).getByText("Active")).toBeTruthy();
    const efua = within(tbl).getByText("efua@acme.test").closest("tr") as HTMLElement;
    expect(within(efua).getByText("Invited")).toBeTruthy();
    expect(within(efua).getByText("Never")).toBeTruthy();

    const rail = screen.getByRole("navigation", { name: "Administration" });
    expect(within(rail).getByRole("link", { name: "Users" }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByText("Showing 4 of 4")).toBeTruthy();
  });

  it("filters by status tab and keeps the tab in the URL", async () => {
    const router = await renderAt("/admin/users");
    await table();
    fireEvent.click(screen.getByRole("tab", { name: "Invited" }));

    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).toEqual(["efua@acme.test"]));
    expect(router.state.location.search).toEqual({ status: "invited" });
    fireEvent.click(screen.getByRole("tab", { name: "Suspended" }));
    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).toEqual(["chidi@acme.test"]));
  });

  it("restores the tab and search from the URL", async () => {
    await renderAt("/admin/users?status=active&q=bola");
    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).toEqual(["bola@acme.test"]));
    expect((screen.getByRole("searchbox", { name: "Search users" }) as HTMLInputElement).value).toBe("bola");
    expect(screen.getByRole("tab", { name: "Active" }).getAttribute("aria-selected")).toBe("true");
  });

  it("searches by name or email after a pause and shows an empty state for no match", async () => {
    const router = await renderAt("/admin/users");
    await table();
    const search = screen.getByRole("searchbox", { name: "Search users" });

    fireEvent.change(search, { target: { value: "Chidi" } });
    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).toEqual(["chidi@acme.test"]));
    expect(router.state.location.search).toEqual({ q: "Chidi" });

    fireEvent.change(search, { target: { value: "nobody" } });
    expect(await screen.findByText("No users found")).toBeTruthy();
  });

  it("keeps a trailing space the user is still typing", async () => {
    await renderAt("/admin/users");
    await table();
    const search = screen.getByRole("searchbox", { name: "Search users" }) as HTMLInputElement;
    fireEvent.change(search, { target: { value: "bola " } });
    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).toEqual(["bola@acme.test"]));
    expect(search.value).toBe("bola ");
  });

  it("pages through a large tenant with Load more", async () => {
    const many: FakeUser[] = Array.from({ length: 60 }, (_, i) => ({
      id: `bulk-${i}`,
      name: `Bulk ${i}`,
      email: `bulk${String(i).padStart(2, "0")}@acme.test`,
      roles: ["user"],
      status: "active",
      lastLoginAt: null,
    }));
    await renderAt("/admin/users", { users: [...USERS, ...many] });
    await table();
    expect(screen.getByText("Showing 50 of 64")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Showing 64 of 64")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("shows a retryable error when the list fails to load", async () => {
    await renderAt("/admin/users", { failAll: true });
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Couldn't load users.")).toBeTruthy();
    expect(within(alert).getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("opens a user's detail page from their row", async () => {
    const router = await renderAt("/admin/users");
    const tbl = await table();
    fireEvent.click(within(tbl).getByText("bola@acme.test"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/users/u-bola"));
    expect(await screen.findByRole("heading", { name: "Bola Active" })).toBeTruthy();
  });
});

describe("invite slide-over", () => {
  async function openInvite() {
    await renderAt("/admin/users");
    await table();
    fireEvent.click(screen.getByRole("button", { name: "Invite user" }));
    return screen.findByRole("dialog", { name: "Invite user" });
  }

  it("invites a new user, who then appears under Invited", async () => {
    const sheet = await openInvite();
    fireEvent.change(within(sheet).getByLabelText(/Email/), { target: { value: "  New.Person@Acme.test " } });
    fireEvent.change(within(sheet).getByLabelText(/Full name/), { target: { value: "New Person" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Send invite" }));

    expect(await within(sheet).findByText("Invitation sent to new.person@acme.test")).toBeTruthy();
    expect(within(sheet).queryByText(/already has a GoERP account/)).toBeNull();
    const invited = backend?.users().find((user) => user.email === "new.person@acme.test");
    expect(invited?.status).toBe("invited");
    expect(invited?.invitation?.role).toBe("user");

    fireEvent.click(within(sheet).getByRole("button", { name: "Done" }));
    fireEvent.click(screen.getByRole("tab", { name: "Invited" }));
    await waitFor(() =>
      expect(rowEmails(screen.getByRole("table"))).toEqual(["efua@acme.test", "new.person@acme.test"]),
    );
  });

  it("says when the invitee already has a GoERP account", async () => {
    await renderAt("/admin/users", { otherTenantAccounts: ["kofi@elsewhere.test"] });
    await table();
    fireEvent.click(screen.getByRole("button", { name: "Invite user" }));
    const sheet = await screen.findByRole("dialog", { name: "Invite user" });
    fireEvent.change(within(sheet).getByLabelText(/Email/), { target: { value: "kofi@elsewhere.test" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Send invite" }));

    expect(
      await within(sheet).findByText("This person already has a GoERP account and will be added to this organisation."),
    ).toBeTruthy();
  });

  it("shows field errors for a missing email and an existing member", async () => {
    const sheet = await openInvite();
    fireEvent.click(within(sheet).getByRole("button", { name: "Send invite" }));
    expect(await within(sheet).findByText("Enter an email address.")).toBeTruthy();

    fireEvent.change(within(sheet).getByLabelText(/Email/), { target: { value: "bola@acme.test" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Send invite" }));
    expect(await within(sheet).findByText("This person is already a member of this organisation.")).toBeTruthy();
    expect(backend?.requests.filter((request) => request === "POST /users/invite")).toHaveLength(1);
  });

  it("sends once when the form is submitted twice in a row", async () => {
    const sheet = await openInvite();
    fireEvent.change(within(sheet).getByLabelText(/Email/), { target: { value: "twice@acme.test" } });
    const form = within(sheet).getByRole("button", { name: "Send invite" }).closest("form") as HTMLFormElement;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(await within(sheet).findByText("Invitation sent to twice@acme.test")).toBeTruthy();
    expect(backend?.requests.filter((request) => request === "POST /users/invite")).toHaveLength(1);
  });
});

describe("/admin/users/$userId", () => {
  async function renderUser(id: string, options: Partial<FakeBackendOptions> = {}) {
    const router = await renderAt(`/admin/users/${id}`, options);
    await screen.findByRole("heading", { level: 1 });
    return router;
  }

  it("shows the user's contact details, roles and sessions", async () => {
    await renderUser("u-bola");
    expect(screen.getByText("bola@acme.test")).toBeTruthy();
    expect(screen.getByText("+233 20 000 0000")).toBeTruthy();
    expect(await screen.findByText("Chrome on Windows")).toBeTruthy();
    expect(screen.getByText("Safari on iOS")).toBeTruthy();
    expect(screen.getByText("Portal")).toBeTruthy();
  });

  it("adds and removes a role without a reload", async () => {
    await renderUser("u-chidi");
    fireEvent.click(screen.getByRole("combobox", { name: "Add role" }));
    fireEvent.click(await screen.findByRole("option", { name: "Portal" }));
    fireEvent.click(screen.getByRole("button", { name: "Add role" }));
    await waitFor(() =>
      expect(backend?.users().find((user) => user.id === "u-chidi")?.roles).toEqual(["portal", "user"]),
    );
    const portalRow = (await screen.findByText("Portal", { selector: "span" })).closest("li") as HTMLElement;

    fireEvent.click(within(portalRow).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(backend?.users().find((user) => user.id === "u-chidi")?.roles).toEqual(["user"]));
    await waitFor(() => expect(screen.queryByText("Portal", { selector: "span" })).toBeNull());
  });

  it("keeps a member's last role", async () => {
    await renderUser("u-chidi");
    expect(screen.getByText("A member needs at least one role.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Remove" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("suspends with a reason and unsuspends", async () => {
    await renderUser("u-bola");
    fireEvent.click(screen.getByRole("button", { name: "Suspend user" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/every organisation they belong to/)).toBeTruthy();
    const confirm = within(dialog).getByRole("button", { name: "Suspend user" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Left the company" } });
    fireEvent.click(confirm);

    expect(await screen.findByRole("button", { name: "Unsuspend user" })).toBeTruthy();
    expect(screen.getByText("Suspended")).toBeTruthy();
    expect(backend?.requests).toContain("POST /admin/users/u-bola/suspend");

    fireEvent.click(screen.getByRole("button", { name: "Unsuspend user" }));
    expect(await screen.findByRole("button", { name: "Suspend user" })).toBeTruthy();
    expect(backend?.users().find((user) => user.id === "u-bola")?.status).toBe("active");
  });

  it("resends an invitation and shows it instead of roles and sessions", async () => {
    await renderUser("u-efua");
    expect(screen.getByText(/Invited as User/)).toBeTruthy();
    expect(screen.queryByText("Active sessions")).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete user" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Resend invite" }));
    await waitFor(() => expect(backend?.requests).toContain("POST /users/invitations/inv-efua/resend"));
  });

  it("revokes a session", async () => {
    await renderUser("u-bola");
    const laptop = (await screen.findByText("Chrome on Windows")).closest("li") as HTMLElement;
    fireEvent.click(within(laptop).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(screen.queryByText("Chrome on Windows")).toBeNull());
    expect(backend?.sessions("u-bola").map((session) => session.id)).toEqual(["fam-phone"]);
  });

  it("deletes only once the email is typed, then returns to the list", async () => {
    const router = await renderUser("u-bola");
    fireEvent.click(screen.getByRole("button", { name: "Delete user" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/every organisation they belong to/)).toBeTruthy();
    const confirm = within(dialog).getByRole("button", { name: "Delete user" }) as HTMLButtonElement;
    const typing = within(dialog).getByRole("textbox");

    fireEvent.change(typing, { target: { value: "bola@acme" } });
    expect(confirm.disabled).toBe(true);
    fireEvent.change(typing, { target: { value: "bola@acme.test" } });
    expect(confirm.disabled).toBe(false);
    fireEvent.click(confirm);

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/users"));
    expect(backend?.users().some((user) => user.id === "u-bola")).toBe(false);
    expect(backend?.requests.filter((request) => request === "GET /admin/users/u-bola")).toHaveLength(1);
    await waitFor(() => expect(rowEmails(screen.getByRole("table"))).not.toContain("bola@acme.test"));
  });

  it("offers no suspend or delete on the admin's own account", async () => {
    await renderUser("me");
    expect(screen.queryByRole("button", { name: "Suspend user" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete user" })).toBeNull();
  });

  it("shows a not-found state for an unknown user", async () => {
    await renderAt("/admin/users/nobody");
    expect(await screen.findByText("User not found")).toBeTruthy();
  });
});
