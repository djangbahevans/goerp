import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { AdminNotificationTemplatesPage } from "./admin-notification-templates-page.js";
import { fakeTemplatesBackend, withTemplatesShell } from "./notification-templates-story-fixtures.js";

const meta: Meta<typeof AdminNotificationTemplatesPage> = {
  title: "Admin/Notification templates",
  component: AdminNotificationTemplatesPage,
  decorators: [withTemplatesShell],
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof AdminNotificationTemplatesPage>;

export const Default: Story = {
  name: "defaults, overrides and a type with no templates",
  beforeEach: fakeTemplatesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    expect(canvas.getAllByText("Customised")).toHaveLength(2);
    expect(canvas.getByText("No templates. Sent with its label as the title.")).toBeInTheDocument();
  },
};

export const CustomisedOnly: Story = {
  name: "filtered to customised templates",
  beforeEach: fakeTemplatesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("checkbox", { name: "Customised only" }));
    expect(canvas.queryByText("Default")).not.toBeInTheDocument();
  },
};

export const NoMatch: Story = {
  name: "search matches nothing",
  beforeEach: fakeTemplatesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    await userEvent.type(canvas.getByRole("searchbox", { name: "Search notifications" }), "invoice");
    expect(canvas.getByText("No templates match")).toBeInTheDocument();
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeTemplatesBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() =>
      expect(within(canvasElement).getByText("Couldn't load notification templates.")).toBeInTheDocument(),
    );
  },
};
