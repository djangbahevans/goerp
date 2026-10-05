import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type FakeNotificationTemplatesBackend,
  type FakeNotificationTemplatesOptions,
  installFakeNotificationTemplatesBackend,
} from "../../admin/notification-templates/fake-notification-templates-backend.js";
import { FIXTURE_TYPES, fixtureTemplates } from "../../admin/notification-templates/notification-templates-fixtures.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

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
  availableLocales: ["en", "fr", "pt-BR"],
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

let backend: FakeNotificationTemplatesBackend | null = null;

async function renderPage(options: Partial<FakeNotificationTemplatesOptions> = {}) {
  backend = installFakeNotificationTemplatesBackend({
    types: FIXTURE_TYPES,
    templates: fixtureTemplates(),
    ...options,
  });
  const router = createRouter({
    routeTree,
    context: { auth: AUTH },
    history: createMemoryHistory({ initialEntries: ["/admin/settings/notifications"] }),
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
  if (!options.failAll) await screen.findByRole("table");
  return router;
}

afterEach(() => {
  cleanup();
  backend?.restore();
  backend = null;
  vi.restoreAllMocks();
});

function row(menuLabel: string): HTMLElement {
  return screen.getByRole("button", { name: menuLabel }).closest("tr") as HTMLElement;
}

async function chooseMenuItem(menuLabel: string, item: string) {
  fireEvent.click(screen.getByRole("button", { name: menuLabel }));
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
}

async function openEditor(menuLabel: string): Promise<HTMLElement> {
  await chooseMenuItem(menuLabel, "Edit");
  const sheet = await screen.findByRole("dialog", { name: "Edit template" });
  await within(sheet).findByRole("button", { name: "Save" });
  return sheet;
}

function requestsTo(method: string) {
  return backend?.requests.filter((r) => r.method === method) ?? [];
}

describe("/admin/settings/notifications", () => {
  it("groups every declared type, engine types first, with each template's status", async () => {
    await renderPage();

    const groups = screen.getAllByRole("rowheader").map((th) => th.textContent);
    expect(groups).toEqual(["General", "sales"]);
    expect(within(row("Order Confirmed, Email, English actions")).getByText("Customised")).toBeTruthy();
    expect(within(row("Order Confirmed, In-app, English actions")).getByText("Default")).toBeTruthy();
    expect(
      within(row("Quote Expiring actions")).getByText("No templates. Sent with its label as the title."),
    ).toBeTruthy();

    const rail = screen.getByRole("navigation", { name: "Administration" });
    expect(within(rail).getByRole("link", { name: "Notification templates" }).getAttribute("aria-current")).toBe(
      "page",
    );
  });

  it("filters by search and to customised templates, and clears an empty result", async () => {
    await renderPage();

    fireEvent.change(screen.getByRole("searchbox", { name: "Search notifications" }), {
      target: { value: "activity" },
    });
    expect(screen.getAllByRole("rowheader").map((th) => th.textContent)).toEqual(["General"]);

    fireEvent.click(screen.getByRole("checkbox", { name: "Customised only" }));
    expect(screen.getByText("No templates match")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getAllByRole("rowheader")).toHaveLength(2);
  });

  it("resets an override to its default from the row menu", async () => {
    await renderPage();

    await chooseMenuItem("Order Confirmed, Email, English actions", "Reset to default");
    expect(await screen.findByText("Your version is deleted. New notifications use the default.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));

    await waitFor(() =>
      expect(within(row("Order Confirmed, Email, English actions")).getByText("Default")).toBeTruthy(),
    );
    expect(requestsTo("DELETE").map((r) => r.path)).toEqual([
      "/admin/settings/notification-templates/sales.order_confirmed/email/en",
    ]);
  });

  it("deletes an override with no default and moves focus to the next row's menu", async () => {
    await renderPage();

    await chooseMenuItem("Order Confirmed, SMS, French actions", "Delete template");
    expect(await screen.findByText("Members who use French get the English template instead.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Order Confirmed, SMS, French actions" })).toBeNull(),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("button", { name: "Quote Expiring actions" })),
    );
  });

  it("edits the effective template beside its default and saves the override", async () => {
    await renderPage();
    const sheet = await openEditor("Order Confirmed, In-app, English actions");

    expect(within(sheet).getByText("Order Confirmed · In-app · English")).toBeTruthy();
    const title = within(sheet).getByLabelText("Title") as HTMLInputElement;
    expect(title.value).toBe("Order {{.OrderReference}} confirmed");
    expect(within(sheet).getByRole("button", { name: "Save" })).toHaveProperty("disabled", true);

    fireEvent.change(title, { target: { value: "Thanks for order {{.OrderReference}}" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit template" })).toBeNull());
    expect(requestsTo("PUT").at(-1)?.body).toEqual({
      title_template: "Thanks for order {{.OrderReference}}",
      body_template: "Total {{.AmountTotal}}",
      action_url_template: "",
      icon: "shopping-cart",
    });
    await waitFor(() =>
      expect(within(row("Order Confirmed, In-app, English actions")).getByText("Customised")).toBeTruthy(),
    );
  });

  it("shows the engine's parse error on the field it names and focuses it", async () => {
    await renderPage();
    const sheet = await openEditor("Order Confirmed, In-app, English actions");

    const body = within(sheet).getByLabelText("Body");
    fireEvent.change(body, { target: { value: "Total {{.AmountTotal" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(await within(sheet).findByText(/This doesn't work as a template: .*unclosed action/)).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(body));
  });

  it("asks before discarding unsaved changes", async () => {
    await renderPage();
    const sheet = await openEditor("Order Confirmed, In-app, English actions");

    fireEvent.change(within(sheet).getByLabelText("Title"), { target: { value: "Changed" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    fireEvent.click(await screen.findByRole("button", { name: "Keep editing" }));
    expect(screen.getByRole("dialog", { name: "Edit template" })).toBeTruthy();

    fireEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    fireEvent.click(await screen.findByRole("button", { name: "Discard" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit template" })).toBeNull());
    expect(requestsTo("PUT")).toHaveLength(0);
  });

  it("previews an SMS and counts its characters and segments from the engine", async () => {
    await renderPage();
    const sheet = await openEditor("Order Confirmed, SMS, English actions");

    expect(await within(sheet).findByText("Acme: Order OrderReference confirmed.", {}, { timeout: 2000 })).toBeTruthy();
    expect(within(sheet).getByText("37 characters · 1 segment")).toBeTruthy();

    fireEvent.change(within(sheet).getByLabelText("Message"), { target: { value: "x".repeat(200) } });
    await waitFor(() => expect(within(sheet).getByText(/200 characters · Sent as 2 messages/)).toBeTruthy(), {
      timeout: 2000,
    });
    expect(within(sheet).getByText("Now sent as 2 messages.")).toBeTruthy();
  });

  it("adds a template for a new language, pre-filled from its fallback", async () => {
    await renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Add template" }));
    const sheet = await screen.findByRole("dialog", { name: "Add template" });
    fireEvent.click(within(sheet).getByLabelText("Notification"));
    fireEvent.click(await screen.findByRole("option", { name: "Order Confirmed" }));
    fireEvent.click(within(sheet).getByLabelText("Channel"));
    fireEvent.click(await screen.findByRole("option", { name: "In-app" }));
    fireEvent.focus(within(sheet).getByLabelText("Language"));
    fireEvent.click(await screen.findByRole("option", { name: "Brazilian Portuguese" }));

    const title = (await within(sheet).findByLabelText("Title")) as HTMLInputElement;
    expect(title.value).toBe("Order {{.OrderReference}} confirmed");
    fireEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add template" })).toBeNull());
    expect(requestsTo("PUT").at(-1)?.path).toBe(
      "/admin/settings/notification-templates/sales.order_confirmed/in_app/pt-BR",
    );
  });

  it("moves focus on when a reset removes its row from the customised-only view", async () => {
    await renderPage();
    fireEvent.click(screen.getByRole("checkbox", { name: "Customised only" }));

    await chooseMenuItem("Order Confirmed, Email, English actions", "Reset to default");
    fireEvent.click(await screen.findByRole("button", { name: "Reset" }));

    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("button", { name: "Order Confirmed, SMS, French actions" })),
    );
  });

  it("closes the sheet after deleting a template reached through add mode", async () => {
    await renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Add template" }));
    const sheet = await screen.findByRole("dialog", { name: "Add template" });
    fireEvent.click(within(sheet).getByLabelText("Notification"));
    fireEvent.click(await screen.findByRole("option", { name: "Order Confirmed" }));
    fireEvent.click(within(sheet).getByLabelText("Channel"));
    fireEvent.click(await screen.findByRole("option", { name: "SMS" }));
    fireEvent.focus(within(sheet).getByLabelText("Language"));
    fireEvent.click(await screen.findByRole("option", { name: "French" }));

    fireEvent.click(await within(sheet).findByRole("button", { name: "Delete template" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: /template$/ })).toBeNull());
    expect(requestsTo("DELETE")).toHaveLength(1);
  });

  it("clears the SMS counter when the message is emptied", async () => {
    await renderPage();
    const sheet = await openEditor("Order Confirmed, SMS, English actions");
    expect(await within(sheet).findByText("37 characters · 1 segment", {}, { timeout: 2000 })).toBeTruthy();

    fireEvent.change(within(sheet).getByLabelText("Message"), { target: { value: "" } });
    await waitFor(() => expect(within(sheet).queryByText(/characters ·/)).toBeNull(), { timeout: 2000 });
    expect(within(sheet).getByText("Add some content to see a preview.")).toBeTruthy();
  });

  it("shows a retryable error when the list fails to load", async () => {
    await renderPage({ failAll: true });
    expect(await screen.findByText("Couldn't load notification templates.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });
});
