import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { NotificationTemplateSheet } from "./notification-template-sheet.js";
import { fakeTemplatesBackend, withTemplatesShell } from "./notification-templates-story-fixtures.js";

const meta: Meta<typeof NotificationTemplateSheet> = {
  title: "Admin/Notification templates/Template sheet",
  component: NotificationTemplateSheet,
  decorators: [withTemplatesShell],
  parameters: { layout: "fullscreen" },
  args: { open: true, onClose: () => {} },
  beforeEach: fakeTemplatesBackend(),
};

export default meta;

type Story = StoryObj<typeof NotificationTemplateSheet>;

const PREVIEW_TIMEOUT = { timeout: 3000 };

async function sheet(name = "Edit template") {
  const dialog = await screen.findByRole("dialog", { name });
  await within(dialog).findByRole("button", { name: "Save" });
  return within(dialog);
}

export const InApp: Story = {
  name: "in-app, default template",
  args: { target: { type: "sales.order_confirmed", channel: "in_app", locale: "en" } },
  play: async () => {
    const dialog = await sheet();
    await waitFor(
      () => expect(dialog.getByText("Order OrderReference confirmed")).toBeInTheDocument(),
      PREVIEW_TIMEOUT,
    );
  },
};

export const EmailCustomised: Story = {
  name: "email, customised",
  args: { target: { type: "sales.order_confirmed", channel: "email", locale: "en" } },
  play: async () => {
    const dialog = await sheet();
    expect(dialog.getByRole("button", { name: "Reset to default" })).toBeInTheDocument();
    await waitFor(() => expect(dialog.getByTitle("Email preview")).toBeInTheDocument(), PREVIEW_TIMEOUT);
  },
};

export const EmailLayoutBroken: Story = {
  name: "email, tenant layout doesn't render",
  args: { target: { type: "sales.order_confirmed", channel: "email", locale: "en" } },
  beforeEach: fakeTemplatesBackend({ brokenLayout: true }),
  play: async () => {
    const dialog = await sheet();
    await waitFor(
      () => expect(dialog.getByText(/Your email layout can't be rendered/)).toBeInTheDocument(),
      PREVIEW_TIMEOUT,
    );
  },
};

export const SMSMultiSegment: Story = {
  name: "SMS past one segment",
  args: { target: { type: "sales.order_confirmed", channel: "sms", locale: "en" } },
  play: async () => {
    const dialog = await sheet();
    await userEvent.clear(dialog.getByLabelText("Message"));
    await userEvent.type(dialog.getByLabelText("Message"), "Your order is confirmed. ".repeat(8));
    await waitFor(() => expect(dialog.getByText(/Sent as 2 messages/)).toBeInTheDocument(), PREVIEW_TIMEOUT);
  },
};

export const SMSNoDefault: Story = {
  name: "SMS in a language with no default",
  args: { target: { type: "sales.order_confirmed", channel: "sms", locale: "fr" } },
  play: async () => {
    const dialog = await sheet();
    expect(dialog.getByRole("button", { name: "Delete template" })).toBeInTheDocument();
    await userEvent.click(dialog.getByRole("tab", { name: "Default" }));
    expect(dialog.getByText(/members who use it get the English template/)).toBeInTheDocument();
  },
};

export const TemplateError: Story = {
  name: "template doesn't render",
  args: { target: { type: "sales.order_confirmed", channel: "in_app", locale: "en" } },
  play: async () => {
    const dialog = await sheet();
    // user-event types "{{" as one literal "{".
    await userEvent.type(dialog.getByLabelText("Body"), " {{{{.Paid");
    await waitFor(
      () => expect(dialog.getByText("Fix Body to update the preview.")).toBeInTheDocument(),
      PREVIEW_TIMEOUT,
    );
  },
};

export const Push: Story = {
  name: "push, falling back to the in-app title",
  args: { target: { type: "engine.activity_assigned", channel: "push", locale: "en" }, open: true },
  beforeEach: fakeTemplatesBackend({
    templates: [
      {
        type: "engine.activity_assigned",
        channel: "push",
        locale: "en",
        override: { push_body_template: "{{.ActivitySummary}} is yours" },
      },
    ],
  }),
  play: async () => {
    const dialog = await sheet();
    await waitFor(() => expect(dialog.getByText("Uses the in-app title")).toBeInTheDocument(), PREVIEW_TIMEOUT);
  },
};

export const Variables: Story = {
  name: "variables and sample values",
  args: { target: { type: "sales.order_confirmed", channel: "in_app", locale: "en" } },
  play: async () => {
    const dialog = await sheet();
    await userEvent.click(dialog.getByRole("tab", { name: "Variables" }));
    expect(dialog.getByText("{{.OrderReference}}")).toBeInTheDocument();
    expect(dialog.getByRole("checkbox", { name: "Sample Paid" })).toBeChecked();
  },
};

export const SaveFailed: Story = {
  name: "save failed",
  args: { target: { type: "sales.order_confirmed", channel: "in_app", locale: "en" } },
  beforeEach: fakeTemplatesBackend({ failSave: true }),
  play: async () => {
    const dialog = await sheet();
    await userEvent.type(dialog.getByLabelText("Title"), "!");
    await userEvent.click(dialog.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(dialog.getByText("Couldn't save this template. Try again.")).toBeInTheDocument());
  },
};

export const AddMode: Story = {
  name: "add mode, type pre-selected",
  args: { target: null, initialType: "sales.quote_expiring" },
  play: async () => {
    const dialog = within(await screen.findByRole("dialog", { name: "Add template" }));
    expect(dialog.getByLabelText("Notification")).toHaveTextContent("Quote Expiring");
  },
};
