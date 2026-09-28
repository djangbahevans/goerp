import { toast } from "@goerp/sdk/notifications";
import {
  buildEmptyViewRegistry,
  type LoadStatus,
  type NotificationTypeGroup,
  ViewRegistryContext,
  ViewRegistryStatusContext,
} from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  applyPreferencesPatch,
  type NotificationPreferences,
  type NotificationPreferencesClient,
  type NotificationPreferencesPatch,
} from "./notification-preferences.js";
import { NotificationsPage } from "./notifications-page.js";

const GROUPS: NotificationTypeGroup[] = [
  {
    module: "sales",
    displayName: "Sales",
    types: [
      {
        type: "sales.order_confirmed",
        label: "Order confirmed",
        description: "Sent when a sales order is confirmed",
        availableChannels: ["in_app", "email", "sms", "push"],
      },
      {
        type: "sales.invoice_overdue",
        label: "Invoice overdue",
        description: null,
        availableChannels: ["in_app", "email"],
      },
    ],
  },
];

const PREFS: NotificationPreferences = {
  availableChannels: ["in_app", "email", "sms"],
  global: { email: true, sms: false, push: true },
  types: { "sales.invoice_overdue": { email: false, sms: false, push: true } },
};

// An in-memory GET/PATCH /_notif/preferences.
function fakeServer(initial: NotificationPreferences = PREFS) {
  let stored = initial;
  const client: NotificationPreferencesClient = {
    get: vi.fn(async () => stored),
    update: vi.fn(async (patch: NotificationPreferencesPatch) => {
      stored = applyPreferencesPatch(stored, patch);
      return stored;
    }),
  };
  return client;
}

function renderPage({
  client = fakeServer(),
  groups = GROUPS,
  status = "ready",
  reload = vi.fn(),
}: {
  client?: NotificationPreferencesClient;
  groups?: NotificationTypeGroup[];
  status?: LoadStatus;
  reload?: () => void;
} = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const registry = { ...buildEmptyViewRegistry(), notificationTypes: groups };
  const result = render(
    <QueryClientProvider client={queryClient}>
      <ViewRegistryContext.Provider value={registry}>
        <ViewRegistryStatusContext.Provider value={status}>
          <NotificationsPage client={client} reload={reload} />
        </ViewRegistryStatusContext.Provider>
      </ViewRegistryContext.Provider>
    </QueryClientProvider>,
  );
  return { client, ...result };
}

function checkbox(name: string): HTMLInputElement {
  return screen.getByRole("checkbox", { name }) as HTMLInputElement;
}

function globalSwitch(label: string): HTMLInputElement {
  return screen.getByRole("switch", { name: label }) as HTMLInputElement;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("NotificationsPage", () => {
  it("shows global toggles and columns only for the tenant's available channels", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Sales" });

    expect(globalSwitch("Email").checked).toBe(true);
    expect(globalSwitch("SMS").checked).toBe(false);
    expect(screen.queryByRole("switch", { name: "Push" })).toBeNull();

    const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual(["Notification type", "In-app", "Email", "SMS"]);
    expect(screen.queryByRole("checkbox", { name: /^Push for/ })).toBeNull();
  });

  it("hides a channel the tenant lost, even when the user's saved preference turned it on", async () => {
    renderPage({
      client: fakeServer({
        availableChannels: ["in_app", "email"],
        global: { email: true, sms: true, push: true },
        types: { "sales.order_confirmed": { email: true, sms: true, push: true } },
      }),
    });
    await screen.findByRole("heading", { name: "Sales" });

    expect(screen.queryByRole("switch", { name: "SMS" })).toBeNull();
    expect(screen.queryByRole("checkbox", { name: /^SMS for/ })).toBeNull();
  });

  it("shows each type's own settings, or global for a type without any", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Sales" });

    expect(checkbox("Email for Order confirmed").checked).toBe(true);
    expect(checkbox("Email for Invoice overdue").checked).toBe(false);
  });

  it("marks a channel the type doesn't offer as not available, and in-app as always on", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Sales" });

    const overdue = screen.getByText("Invoice overdue").closest("tr") as HTMLElement;
    expect(within(overdue).getByText("Not available")).toBeTruthy();
    expect(within(overdue).queryByRole("checkbox", { name: "SMS for Invoice overdue" })).toBeNull();
    expect(within(overdue).getByText("Always")).toBeTruthy();
  });

  it("saves a per-type toggle with only that change, and keeps it across a reload", async () => {
    const client = fakeServer();
    const { unmount } = renderPage({ client });
    await screen.findByRole("heading", { name: "Sales" });

    fireEvent.click(checkbox("Email for Order confirmed"));
    await waitFor(() => expect(checkbox("Email for Order confirmed").checked).toBe(false));
    await waitFor(() =>
      expect(client.update).toHaveBeenCalledWith({ types: { "sales.order_confirmed": { email: false } } }),
    );

    unmount();
    renderPage({ client });
    await screen.findByRole("heading", { name: "Sales" });
    expect(checkbox("Email for Order confirmed").checked).toBe(false);
    expect(checkbox("SMS for Order confirmed").checked).toBe(false);
  });

  it("saves a global toggle, which types without their own settings follow", async () => {
    const client = fakeServer();
    renderPage({ client });
    await screen.findByRole("heading", { name: "Sales" });

    fireEvent.click(globalSwitch("SMS"));
    await waitFor(() => expect(globalSwitch("SMS").checked).toBe(true));
    expect(checkbox("SMS for Order confirmed").checked).toBe(true);
    await waitFor(() => expect(client.update).toHaveBeenCalledWith({ global: { sms: true } }));
  });

  it("reverts a failed save and says so", async () => {
    const error = vi.spyOn(toast, "error").mockImplementation(() => "");
    const client = fakeServer();
    let fail: (err: Error) => void = () => {};
    vi.mocked(client.update).mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          fail = reject;
        }),
    );
    renderPage({ client });
    await screen.findByRole("heading", { name: "Sales" });

    fireEvent.click(checkbox("Email for Order confirmed"));
    await waitFor(() => expect(checkbox("Email for Order confirmed").checked).toBe(false));
    fail(new Error("network"));
    await waitFor(() => expect(checkbox("Email for Order confirmed").checked).toBe(true));
    expect(error).toHaveBeenCalledWith("Couldn't save your notification settings. Try again.");
  });

  it("doesn't revert a toggle changed again after a save that later fails", async () => {
    const error = vi.spyOn(toast, "error").mockImplementation(() => "");
    const client = fakeServer();
    let failFirst: (err: Error) => void = () => {};
    vi.mocked(client.update).mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          failFirst = reject;
        }),
    );
    renderPage({ client });
    await screen.findByRole("heading", { name: "Sales" });

    fireEvent.click(checkbox("Email for Order confirmed"));
    await waitFor(() => expect(checkbox("Email for Order confirmed").checked).toBe(false));
    fireEvent.click(checkbox("Email for Order confirmed"));
    await waitFor(() => expect(client.update).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(checkbox("Email for Order confirmed").checked).toBe(true));
    failFirst(new Error("network"));

    await waitFor(() => expect(client.get).toHaveBeenCalledTimes(1));
    expect(checkbox("Email for Order confirmed").checked).toBe(true);
    expect(error).not.toHaveBeenCalled();
  });

  it("shows an empty state when no module declares a notification type", async () => {
    renderPage({ groups: [] });
    expect(await screen.findByText("No notification types")).toBeTruthy();
    expect(globalSwitch("Email")).toBeTruthy();
  });

  it("offers a retry when the preferences fail to load", async () => {
    const client = fakeServer();
    vi.mocked(client.get).mockRejectedValueOnce(new Error("network"));
    renderPage({ client });

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Couldn't load your notification settings.");
    fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "Sales" })).toBeTruthy();
  });
});

describe("NotificationsPage schema failure", () => {
  it("reloads the page to retry a failed schema load", async () => {
    const reload = vi.fn();
    renderPage({ status: "error", reload });

    const alert = await screen.findByRole("alert");
    fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("NotificationsPage with a type set back to global", () => {
  it("follows a later global change for that type", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Sales" });

    fireEvent.click(checkbox("Email for Invoice overdue"));
    await waitFor(() => expect(checkbox("Email for Invoice overdue").checked).toBe(true));
    fireEvent.click(globalSwitch("Email"));
    await waitFor(() => expect(globalSwitch("Email").checked).toBe(false));
    expect(checkbox("Email for Invoice overdue").checked).toBe(false);
  });
});

describe("applyPreferencesPatch", () => {
  it("fills a new type's unpatched channels from the updated global settings", () => {
    const next = applyPreferencesPatch(PREFS, {
      global: { push: false },
      types: { "sales.order_confirmed": { email: false } },
    });
    expect(next.global).toEqual({ email: true, sms: false, push: false });
    expect(next.types["sales.order_confirmed"]).toEqual({ email: false, sms: false, push: false });
    expect(next.types["sales.invoice_overdue"]).toEqual({ email: false, sms: false, push: true });
  });

  it("drops a type's own settings once a patch makes them equal global", () => {
    const next = applyPreferencesPatch(PREFS, { types: { "sales.invoice_overdue": { email: true } } });
    expect(next.types).toEqual({});
  });
});
