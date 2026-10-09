import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AFRICASTALKING, PAYSTACK, TWILIO } from "../../admin/connectors/admin-connectors-story-fixtures.js";
import {
  type FakeConnectorsBackend,
  type FakeConnectorsBackendOptions,
  installFakeAdminConnectorsBackend,
} from "../../admin/connectors/fake-admin-connectors-backend.js";
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

let backend: FakeConnectorsBackend | null = null;

async function renderAt(path: string, options: Partial<FakeConnectorsBackendOptions> = {}) {
  backend = installFakeAdminConnectorsBackend({
    connectors: [PAYSTACK, TWILIO, AFRICASTALKING],
    primary: { sms_provider: "connector_twilio" },
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

function requests(method: string, path?: string) {
  return (backend?.requests ?? []).filter((r) => r.method === method && (path === undefined || r.path === path));
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Save configuration" }) as HTMLButtonElement;
}

describe("/admin/connectors", () => {
  it("lists the connectors with their state, under the Connectors rail entry", async () => {
    await renderAt("/admin/connectors");

    const names = await screen.findAllByRole("heading", { level: 2 });
    expect(names.map((heading) => heading.textContent)).toEqual(["Africa's Talking", "Paystack", "Twilio"]);

    const card = (name: string) => names.find((h) => h.textContent === name)?.closest("li") as HTMLElement;
    expect(within(card("Paystack")).getByText("Version 1.2.0")).toBeTruthy();
    expect(within(card("Paystack")).getByText("Configured")).toBeTruthy();
    expect(within(card("Paystack")).queryByText("Primary")).toBeNull();
    expect(within(card("Africa's Talking")).getByText("Not configured")).toBeTruthy();
    expect(within(card("Twilio")).getByText("Primary")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Connectors" }).getAttribute("aria-current")).toBe("page");
  });

  it("opens a connector from its Configure button", async () => {
    const router = await renderAt("/admin/connectors");
    fireEvent.click(await screen.findByRole("button", { name: "Configure Paystack" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/connectors/connector_paystack"));
    expect(await screen.findByRole("heading", { name: "Paystack" })).toBeTruthy();
  });

  it("offers a retry when the list fails to load", async () => {
    await renderAt("/admin/connectors", { failAll: true });
    expect(await screen.findByText("Couldn't load connectors.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("says so when no connector is installed", async () => {
    await renderAt("/admin/connectors", { connectors: [] });
    expect(await screen.findByText("No connectors installed")).toBeTruthy();
  });
});

describe("/admin/connectors/:name", () => {
  it("renders one widget per field type under its category heading", async () => {
    await renderAt("/admin/connectors/connector_paystack");

    expect(await screen.findByRole("heading", { name: "Paystack" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "API Credentials" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Options" })).toBeTruthy();

    expect(screen.getByLabelText("Secret Key").getAttribute("type")).toBe("password");
    expect((screen.getByRole("switch") as HTMLInputElement).checked).toBe(false);
    expect(screen.getByRole("combobox", { name: "Currency" }).textContent).toContain("Ghana cedi");
    expect(screen.getByLabelText("Retries").getAttribute("type")).toBe("number");
    expect((screen.getByLabelText("Retries") as HTMLInputElement).value).toBe("3");
    expect(screen.getByRole("combobox", { name: "Channels" }).textContent).toContain("Card");
    expect(screen.getByLabelText("Reference prefix").getAttribute("type")).toBe("text");
    expect(screen.getByText("Found in your Paystack dashboard.")).toBeTruthy();
  });

  it("never puts an encrypted value in the page, only the mask", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    const secret = (await screen.findByLabelText("Secret Key")) as HTMLInputElement;

    expect(secret.value).toBe("***");
    fireEvent.click(screen.getByRole("button", { name: "Show password" }));
    expect(secret.type).toBe("text");
    expect(secret.value).toBe("***");
    expect(document.body.innerHTML).not.toContain("sk_live_plaintext_value");
    expect(document.body.innerHTML).not.toContain("whsec_generated");
  });

  it("shows a generated value read-only with a Rotate action, never as an input", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    await screen.findByRole("heading", { name: "Paystack" });

    expect(screen.queryByLabelText("Webhook Secret")).toBeNull();
    expect(screen.getByText("••••••••••••")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Rotate" })).toBeTruthy();
  });

  it("rotates a generated value after confirmation", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    fireEvent.click(await screen.findByRole("button", { name: "Rotate" }));
    expect(requests("POST")).toHaveLength(0);

    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Rotate" }));

    await waitFor(() =>
      expect(requests("POST", "/admin/connectors/connector_paystack/config/webhook_secret/rotate")).toHaveLength(1),
    );
  });

  it("saves only what changed, qualified with the module name, and then resets", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    await screen.findByRole("heading", { name: "Paystack" });
    expect(saveButton().disabled).toBe(true);

    fireEvent.click(await screen.findByRole("switch"));
    fireEvent.change(screen.getByLabelText("Retries"), { target: { value: "5" } });
    expect(saveButton().disabled).toBe(false);
    fireEvent.click(saveButton());

    await waitFor(() => expect(requests("PATCH", "/admin/config")).toHaveLength(1));
    expect(requests("PATCH", "/admin/config")[0]?.body).toEqual({
      "connector_paystack.test_mode": true,
      "connector_paystack.retries": 5,
    });
    await waitFor(() => expect(saveButton().disabled).toBe(true));
    expect((screen.getByRole("switch") as HTMLInputElement).checked).toBe(true);
  });

  it("sends a newly typed secret and not the unchanged mask", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    fireEvent.change(await screen.findByLabelText("Secret Key"), { target: { value: "sk_live_new" } });
    fireEvent.click(saveButton());

    await waitFor(() => expect(requests("PATCH", "/admin/config")).toHaveLength(1));
    expect(requests("PATCH", "/admin/config")[0]?.body).toEqual({ "connector_paystack.secret_key": "sk_live_new" });
    await waitFor(() => expect(saveButton().disabled).toBe(true));
    expect((screen.getByLabelText("Secret Key") as HTMLInputElement).value).toBe("***");
    expect(document.body.innerHTML).not.toContain("sk_live_new");
  });

  it("rejects an emptied required field without calling the server", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    fireEvent.change(await screen.findByLabelText("Secret Key"), { target: { value: "" } });
    fireEvent.click(saveButton());

    expect(await screen.findByText("This is required.")).toBeTruthy();
    expect(requests("PATCH")).toHaveLength(0);
  });

  it("shows the server's rejection under the field it names", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    fireEvent.change(await screen.findByLabelText("Retries"), { target: { value: "9" } });
    fireEvent.click(saveButton());

    expect(await screen.findByText("must be at most 5")).toBeTruthy();
    expect(saveButton().disabled).toBe(false);
  });

  it("explains that a restart-required change waits for a reload", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    expect(await screen.findByText("A change takes effect after the module reloads.")).toBeTruthy();
  });

  it("shows the webhook URL once the endpoint exists, and copies it", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await renderAt("/admin/connectors/connector_paystack");

    const url = await screen.findByLabelText("Webhook URL");
    expect(url.textContent).toBe(`${window.location.origin}/_webhooks/connector_paystack/tok43paystack`);
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(url.textContent));
  });

  it("shows no webhook URL before the endpoint exists, and mints it on the first complete save", async () => {
    const { webhookToken: _, ...unminted } = PAYSTACK;
    const paystack = { ...unminted, config: PAYSTACK.config.map((e) => ({ ...e })) };
    const secret = paystack.config.find((e) => e.key === "secret_key");
    if (secret) delete secret.stored;
    await renderAt("/admin/connectors/connector_paystack", { connectors: [paystack] });

    await screen.findByRole("heading", { name: "Paystack" });
    expect(screen.queryByLabelText("Webhook URL")).toBeNull();

    fireEvent.change(screen.getByLabelText("Secret Key"), { target: { value: "sk_live_new" } });
    fireEvent.click(saveButton());

    expect((await screen.findByLabelText("Webhook URL")).textContent).toMatch(/\/_webhooks\/connector_paystack\/\w+$/);
  });

  it("revokes the webhook URL after confirmation and then hides it", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    await screen.findByLabelText("Webhook URL");

    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    expect(requests("DELETE")).toHaveLength(0);
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(requests("DELETE", "/admin/connectors/connector_paystack/webhook")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByLabelText("Webhook URL")).toBeNull());
  });

  it("shows no webhook section for a connector that takes no webhooks", async () => {
    await renderAt("/admin/connectors/connector_twilio");
    await screen.findByRole("heading", { name: "Twilio" });
    expect(screen.queryByLabelText("Webhook URL")).toBeNull();
  });

  it("tests the connection and shows the connector's own result inline", async () => {
    await renderAt("/admin/connectors/connector_paystack");
    fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));

    const result = await screen.findByRole("status", { name: "Connection test" });
    expect(within(result).getByText("configured")).toBeTruthy();
    expect(within(result).getByText("true")).toBeTruthy();
    expect(within(result).getByText("test mode")).toBeTruthy();
    expect(requests("GET", "/connectors/connector_paystack/status")).toHaveLength(1);
  });

  it("shows a failed connection test, and hides the button for a connector with no status route", async () => {
    await renderAt("/admin/connectors/connector_africastalking");
    fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));
    expect((await screen.findByRole("status", { name: "Connection test" })).textContent).toContain(
      "provider unreachable",
    );
    cleanup();
    backend?.restore();

    await renderAt("/admin/connectors/connector_twilio");
    await screen.findByRole("heading", { name: "Twilio" });
    expect(screen.queryByRole("button", { name: "Test connection" })).toBeNull();
  });

  it("marks the primary provider with a badge and offers the others a Set as primary button", async () => {
    await renderAt("/admin/connectors/connector_twilio");
    await screen.findByRole("heading", { name: "Twilio" });
    expect(screen.getByText("Primary")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Set as primary provider" })).toBeNull();
    cleanup();
    backend?.restore();

    await renderAt("/admin/connectors/connector_africastalking");
    expect(screen.queryByText("Primary")).toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: "Set as primary provider" }));

    await waitFor(() =>
      expect(requests("PATCH", "/admin/connectors/connector_africastalking/set-primary")).toHaveLength(1),
    );
    expect(await screen.findByText("Primary")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Set as primary provider" })).toBeNull();
  });

  it("selects independent primary providers for a connector with multiple categories", async () => {
    await renderAt("/admin/connectors/connector_twilio", {
      connectors: [
        { ...TWILIO, categories: ["sms_provider", "push_provider"] },
        { ...AFRICASTALKING, categories: ["sms_provider", "push_provider"] },
      ],
      primary: { sms_provider: "connector_africastalking", push_provider: "connector_africastalking" },
    });

    fireEvent.click(await screen.findByRole("button", { name: "Set as primary SMS provider" }));
    expect(await screen.findByText("Primary SMS provider")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Set as primary push notification provider" })).toBeTruthy();
    expect(requests("PATCH")[0]?.body).toEqual({ category: "sms_provider" });

    fireEvent.click(screen.getByRole("button", { name: "Set as primary push notification provider" }));
    expect(await screen.findByText("Primary push notification provider")).toBeTruthy();
    expect(requests("PATCH")[1]?.body).toEqual({ category: "push_provider" });
  });

  it("hides primary controls for a disabled connector even when its category has several providers", async () => {
    await renderAt("/admin/connectors/connector_twilio", {
      connectors: [{ ...TWILIO, enabled: false }, AFRICASTALKING, { ...AFRICASTALKING, name: "connector_other" }],
    });
    await screen.findByRole("heading", { name: "Twilio" });
    expect(screen.queryByRole("button", { name: "Set as primary provider" })).toBeNull();
  });

  it("hides the primary controls when it is the only provider of its category, and for other connectors", async () => {
    await renderAt("/admin/connectors/connector_twilio", { connectors: [PAYSTACK, TWILIO] });
    await screen.findByRole("heading", { name: "Twilio" });
    expect(screen.queryByText("Primary")).toBeNull();
    expect(screen.queryByRole("button", { name: "Set as primary provider" })).toBeNull();
    cleanup();
    backend?.restore();

    await renderAt("/admin/connectors/connector_paystack");
    await screen.findByRole("heading", { name: "Paystack" });
    expect(screen.queryByRole("button", { name: "Set as primary provider" })).toBeNull();
  });

  it("shows not-found for a connector that isn't installed, with a way back", async () => {
    const router = await renderAt("/admin/connectors/connector_missing");
    expect(await screen.findByText("Connector not found")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to connectors" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/connectors"));
  });

  it("goes back to the list from the link above the form", async () => {
    const router = await renderAt("/admin/connectors/connector_paystack");
    fireEvent.click(await screen.findByRole("button", { name: "All connectors" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/connectors"));
  });
});
