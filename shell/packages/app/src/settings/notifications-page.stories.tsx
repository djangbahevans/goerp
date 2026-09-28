import { Toast } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import {
  buildEmptyViewRegistry,
  type NotificationTypeGroup,
  ViewRegistryContext,
  ViewRegistryStatusContext,
} from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, userEvent, waitFor, within } from "storybook/test";
import {
  applyPreferencesPatch,
  type NotificationPreferences,
  type NotificationPreferencesClient,
} from "./notification-preferences.js";
import { NotificationsPage } from "./notifications-page.js";

const GROUPS: NotificationTypeGroup[] = [
  {
    module: "hr",
    displayName: "HR",
    types: [
      {
        type: "hr.leave_approved",
        label: "Leave approved",
        description: "Sent when a manager approves your leave request",
        availableChannels: ["in_app", "email", "push"],
      },
      {
        type: "hr.payslip_available",
        label: "Payslip available",
        description: null,
        availableChannels: ["in_app", "email"],
      },
    ],
  },
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
        description: "Sent when an invoice passes its due date without payment",
        availableChannels: ["in_app", "email", "sms"],
      },
    ],
  },
];

const PREFS: NotificationPreferences = {
  availableChannels: ["in_app", "email", "sms", "push"],
  global: { email: true, sms: false, push: true },
  types: { "sales.invoice_overdue": { email: true, sms: true, push: true } },
};

// An in-memory client, so a save really changes what the next load returns.
function memoryClient(initial: NotificationPreferences): NotificationPreferencesClient {
  let prefs = initial;
  return {
    get: async () => prefs,
    update: async (patch) => {
      prefs = applyPreferencesPatch(prefs, patch);
      return prefs;
    },
  };
}

function withPage(groups: NotificationTypeGroup[]): Decorator {
  const registry = { ...buildEmptyViewRegistry(), notificationTypes: groups };
  return (Story) => (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ViewRegistryContext.Provider value={registry}>
        <ViewRegistryStatusContext.Provider value="ready">
          <Story />
          <Toast />
        </ViewRegistryStatusContext.Provider>
      </ViewRegistryContext.Provider>
    </QueryClientProvider>
  );
}

const meta: Meta<typeof NotificationsPage> = {
  title: "Settings/NotificationsPage",
  component: NotificationsPage,
};

export default meta;

type Story = StoryObj<typeof NotificationsPage>;

export const AllChannels: Story = {
  name: "every channel available",
  decorators: [withPage(GROUPS)],
  args: { client: memoryClient(PREFS) },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const checkbox = await canvas.findByRole("checkbox", { name: "Email for Order confirmed" });
    await userEvent.click(checkbox);
    await waitFor(() => expect(checkbox).not.toBeChecked());
  },
};

export const NoSmsOrPush: Story = {
  name: "no SMS or push connector installed",
  decorators: [withPage(GROUPS)],
  args: { client: memoryClient({ ...PREFS, availableChannels: ["in_app", "email"] }) },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findByRole("heading", { name: "Sales" });
    await expect(canvas.queryByRole("switch", { name: "SMS" })).not.toBeInTheDocument();
    await expect(canvas.queryByRole("columnheader", { name: "Push" })).not.toBeInTheDocument();
  },
};

export const NoTypes: Story = {
  name: "no module declares a notification type",
  decorators: [withPage([])],
  args: { client: memoryClient(PREFS) },
};

export const SaveError: Story = {
  name: "a failed save reverts the toggle",
  decorators: [withPage(GROUPS)],
  args: {
    client: {
      get: async () => PREFS,
      update: async () => {
        throw new AppError({ code: "internal_error", message: "unavailable", httpStatus: 503 });
      },
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const toggle = await canvas.findByRole("switch", { name: "Email" });
    await userEvent.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    await waitFor(() =>
      expect(
        within(canvasElement.ownerDocument.body).getByText("Couldn't save your notification settings. Try again."),
      ).toBeInTheDocument(),
    );
  },
};
