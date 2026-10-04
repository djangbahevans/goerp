import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  defaultDeliveryWire,
  defaultSettingsWire,
  type FakeTenantSettingsBackend,
  type FakeTenantSettingsOptions,
  installFakeTenantSettingsBackend,
} from "../../admin/settings/fake-tenant-settings-backend.js";
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

let backend: FakeTenantSettingsBackend | null = null;

async function renderSettings(options: FakeTenantSettingsOptions = {}) {
  backend = installFakeTenantSettingsBackend(options);
  const router = createRouter({
    routeTree,
    context: { auth: AUTH },
    history: createMemoryHistory({ initialEntries: ["/admin/settings"] }),
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
  await screen.findByRole("heading", { name: "Email" });
  return router;
}

afterEach(() => {
  cleanup();
  backend?.restore();
  backend = null;
  vi.restoreAllMocks();
});

function section(title: string): HTMLElement {
  return screen.getByRole("heading", { name: title, level: 2 }).closest("section") as HTMLElement;
}

function input(scope: HTMLElement, label: string | RegExp): HTMLInputElement {
  return within(scope).getByLabelText(label) as HTMLInputElement;
}

function type(scope: HTMLElement, label: string | RegExp, value: string) {
  fireEvent.change(input(scope, label), { target: { value } });
}

function save(scope: HTMLElement) {
  fireEvent.click(within(scope).getByRole("button", { name: "Save" }));
}

function lastRequest(method: string, path: string) {
  return backend?.requests.filter((r) => r.method === method && r.path === path).at(-1);
}

function deliveryWith(email: Record<string, unknown>, locked: string[] = []) {
  const wire = defaultDeliveryWire();
  Object.assign(wire.email as Record<string, unknown>, email);
  wire.locked = locked;
  return wire;
}

describe("/admin/settings", () => {
  it("shows every section with its saved values, under the Settings rail entry", async () => {
    await renderSettings();

    const general = section("General");
    expect(input(general, /^Company name/).value).toBe("Acme Ghana Ltd");
    expect(input(general, "Website").value).toBe("https://acme.example");
    for (const title of ["Email", "Security", "Localisation"]) expect(section(title)).toBeTruthy();

    const rail = screen.getByRole("navigation", { name: "Administration" });
    expect(within(rail).getByRole("link", { name: "Settings" }).getAttribute("aria-current")).toBe("page");
  });

  it("saves only the General fields that changed", async () => {
    await renderSettings();
    const general = section("General");
    const saveButton = within(general).getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(saveButton.disabled).toBe(true);

    type(general, /^Company name/, "Acme Holdings");
    save(general);

    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings")?.body).toEqual({ general: { name: "Acme Holdings" } }),
    );
    await waitFor(() =>
      expect((within(section("General")).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(
        true,
      ),
    );
    expect(input(section("General"), /^Company name/).value).toBe("Acme Holdings");
  });

  it("resets the form to the saved value when the server trims an edit", async () => {
    await renderSettings();
    const general = section("General");

    type(general, /^Company name/, "Acme Ghana Ltd  ");
    save(general);

    await waitFor(() => expect(input(section("General"), /^Company name/).value).toBe("Acme Ghana Ltd"));
    expect((within(section("General")).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows a rejected field's message under that field", async () => {
    await renderSettings();
    const general = section("General");

    type(general, "Website", "ftp://acme.example");
    save(general);

    expect(await within(general).findByText("a website is an http or https URL")).toBeTruthy();
    expect(input(general, "Website").getAttribute("aria-invalid")).toBe("true");
  });

  it("keeps a stored API key unless a new one is typed", async () => {
    await renderSettings({
      delivery: deliveryWith({ from_addr: "noreply@acme.test" }),
      secrets: { apiKey: "re_saved" },
    });
    const email = section("Email");
    expect(within(email).getByText("A key is saved. Enter a new one to replace it.")).toBeTruthy();

    type(email, "Sender name", "Acme Notifications");
    save(email);
    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings/notification-delivery")?.body).toEqual({
        email: { from_name: "Acme Notifications" },
      }),
    );
    expect(backend?.state().secrets.apiKey).toBe("re_saved");

    type(section("Email"), "Resend API key", "re_new");
    save(section("Email"));
    await waitFor(() => expect(backend?.state().secrets.apiKey).toBe("re_new"));
    expect(JSON.stringify(backend?.state().delivery)).not.toContain("re_new");
  });

  it("saves under Resend while the hidden SMTP port is empty", async () => {
    await renderSettings({ delivery: deliveryWith({ provider: "smtp", from_addr: "noreply@acme.test" }) });
    const email = section("Email");

    type(email, "Port", "");
    fireEvent.click(within(email).getByLabelText("Resend"));
    type(email, "Sender name", "Acme");
    save(email);

    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings/notification-delivery")?.body).toEqual({
        email: { provider: "resend", from_name: "Acme" },
      }),
    );
  });

  it("sends a test email with the unsaved form and leaves the saved settings alone", async () => {
    const success = vi.spyOn(toast, "success");
    await renderSettings();
    const email = section("Email");

    fireEvent.click(within(email).getByLabelText("SMTP"));
    type(email, "SMTP host", "localhost");
    type(email, "Port", "1025");
    type(email, "From address", "test@acme.test");
    fireEvent.click(within(email).getByRole("button", { name: "Send test email" }));

    await waitFor(() => expect(success).toHaveBeenCalledWith("Test email sent to ada@acme.test."));
    const sent = lastRequest("POST", "/admin/settings/notification-delivery/test-email")?.body as {
      email: Record<string, unknown>;
    };
    expect(sent.email).toMatchObject({ provider: "smtp", from_addr: "test@acme.test" });
    expect(sent.email.smtp).toMatchObject({ host: "localhost", port: 1025 });
    expect((backend?.state().delivery.email as Record<string, unknown> | undefined)?.provider).toBe("resend");
    expect(lastRequest("PATCH", "/admin/settings/notification-delivery")).toBeUndefined();
  });

  it("reports the provider's message when a test email fails", async () => {
    const error = vi.spyOn(toast, "error");
    await renderSettings({
      delivery: deliveryWith({ from_addr: "noreply@acme.test" }),
      secrets: { apiKey: "re_saved" },
      testEmailFailure: "resend 403 validation_error: domain not verified",
    });

    fireEvent.click(within(section("Email")).getByRole("button", { name: "Send test email" }));

    await waitFor(() =>
      expect(error).toHaveBeenCalledWith(
        "The test email wasn't sent: resend 403 validation_error: domain not verified",
      ),
    );
  });

  it("shows the field a test email is missing", async () => {
    await renderSettings();
    const email = section("Email");

    fireEvent.click(within(email).getByRole("button", { name: "Send test email" }));

    expect(await within(email).findByText("a From address is required to send email")).toBeTruthy();
  });

  it("shows a field the platform operator set as read-only", async () => {
    await renderSettings({ delivery: deliveryWith({ provider: "smtp" }, ["email.provider"]) });
    const email = section("Email");

    expect((within(email).getByLabelText("Resend") as HTMLInputElement).disabled).toBe(true);
    expect(within(email).getByText("Set by your platform operator.")).toBeTruthy();
  });

  it("turns a channel off for everyone, apart from the per-type defaults it overrides", async () => {
    await renderSettings();
    const delivery = section("Notification delivery");
    expect(within(delivery).getByText(/These are not defaults\./)).toBeTruthy();
    const smsDefault = within(delivery).getByLabelText("SMS for Order confirmed") as HTMLInputElement;
    expect(smsDefault.disabled).toBe(false);

    fireEvent.click(within(delivery).getByRole("switch", { name: "SMS" }));

    expect(smsDefault.disabled).toBe(true);
    expect(within(delivery).getByText(/SMS is off above, so its defaults have no effect\./)).toBeTruthy();
    save(delivery);
    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings/notification-delivery")?.body).toEqual({
        channels: { sms_enabled: false },
      }),
    );
  });

  it("sets a type's default channels and resets it to the manifest's", async () => {
    await renderSettings();
    let delivery = section("Notification delivery");
    expect(within(delivery).queryByRole("button", { name: "Reset Order confirmed" })).toBeNull();

    fireEvent.click(within(delivery).getByLabelText("Email for Order confirmed"));
    fireEvent.click(within(delivery).getByLabelText("SMS for Order confirmed"));
    save(delivery);
    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings/notification-delivery")?.body).toEqual({
        defaults: { "sales.order_confirmed": ["in_app", "sms"] },
      }),
    );

    delivery = await waitFor(() => {
      const current = section("Notification delivery");
      within(current).getByRole("button", { name: "Reset Order confirmed" });
      return current;
    });
    fireEvent.click(within(delivery).getByRole("button", { name: "Reset Order confirmed" }));
    expect((within(delivery).getByLabelText("Email for Order confirmed") as HTMLInputElement).checked).toBe(true);
    save(delivery);
    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings/notification-delivery")?.body).toEqual({
        defaults: { "sales.order_confirmed": null },
      }),
    );
    expect(backend?.state().delivery.defaults).toEqual({});
  });

  it("treats channels changed back to the manifest's as no default", async () => {
    await renderSettings();
    const delivery = section("Notification delivery");
    const email = within(delivery).getByLabelText("Email for Order confirmed");

    fireEvent.click(email);
    fireEvent.click(email);

    expect(within(delivery).queryByRole("button", { name: "Reset Order confirmed" })).toBeNull();
    expect((within(delivery).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("lists the engine's own types first, as General, then each module's", async () => {
    await renderSettings();
    const delivery = section("Notification delivery");

    const rows = within(within(delivery).getByRole("table"))
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0]?.textContent);
    expect(rows).toEqual(["Activity assigned to youGeneral", "Order confirmedsales", "Quote expiringsales"]);
    expect(within(delivery).queryByLabelText("SMS for Activity assigned to you")).toBeNull();
  });

  it("shows a channel switch the platform operator set as read-only", async () => {
    await renderSettings({ delivery: deliveryWith({}, ["channels.sms_enabled", "defaults"]) });
    const delivery = section("Notification delivery");

    expect((within(delivery).getByRole("switch", { name: "SMS" }) as HTMLInputElement).disabled).toBe(true);
    expect((within(delivery).getByLabelText("Email for Order confirmed") as HTMLInputElement).disabled).toBe(true);
    expect(within(delivery).getAllByText("Set by your platform operator.")).toHaveLength(2);
  });

  it("shows a rejected SMS sender ID under its field", async () => {
    await renderSettings();
    const delivery = section("Notification delivery");

    type(delivery, "SMS sender ID", "Acme Corporation Ltd");
    save(delivery);

    expect(
      await within(delivery).findByText("a sender ID is 1 to 11 letters and digits, or a phone number in E.164 form"),
    ).toBeTruthy();
  });

  it("shows email verification locked by the platform policy", async () => {
    const settings = defaultSettingsWire();
    (settings.security as Record<string, unknown>).email_verification = { policy: "required", required: true };
    await renderSettings({ settings });
    const security = section("Security");

    const toggle = within(security).getByRole("switch", { name: "Require email verification on registration" });
    expect((toggle as HTMLInputElement).disabled).toBe(true);
    expect((toggle as HTMLInputElement).checked).toBe(true);
    expect(within(security).getByText("Required by your platform configuration.")).toBeTruthy();
  });

  it("notes that email verification doesn't affect anything yet when the tenant can choose", async () => {
    await renderSettings();
    const security = section("Security");

    const toggle = within(security).getByRole("switch", { name: "Require email verification on registration" });
    expect((toggle as HTMLInputElement).disabled).toBe(false);
    expect(within(security).getByText("This setting doesn't affect anything yet.")).toBeTruthy();

    fireEvent.click(toggle);
    save(security);
    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings")?.body).toEqual({
        security: { email_verification: { required: false } },
      }),
    );
  });

  it("asks for a grace period only when a password change is required", async () => {
    await renderSettings();
    const security = section("Security");
    expect(within(security).queryByLabelText("Grace period (days)")).toBeNull();

    fireEvent.click(within(security).getByLabelText("Require a change"));
    type(security, "Grace period (days)", "30");
    save(security);

    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings")?.body).toEqual({
        security: { password_policy: { min_length: 12, enforcement: "require", grace_days: 30 } },
      }),
    );
  });

  it("keeps the default language available and saves the rest in the platform's order", async () => {
    await renderSettings();
    const localisation = section("Localisation");

    expect((within(localisation).getByLabelText("English") as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(within(localisation).getByLabelText("العربية"));
    fireEvent.click(within(localisation).getByLabelText("Français"));
    save(localisation);

    await waitFor(() =>
      expect(lastRequest("PATCH", "/admin/settings")?.body).toEqual({
        localisation: { available_locales: ["en", "ar"] },
      }),
    );
  });
});
