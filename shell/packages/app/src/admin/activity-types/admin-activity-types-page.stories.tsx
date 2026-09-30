import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { fakeActivityTypesBackend } from "./activity-types-story-fixtures.js";
import { AdminActivityTypesPage } from "./admin-activity-types-page.js";

const meta: Meta<typeof AdminActivityTypesPage> = {
  title: "Admin/Activity types",
  component: AdminActivityTypesPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof AdminActivityTypesPage>;

export const Default: Story = {
  name: "built-in and custom types",
  beforeEach: fakeActivityTypesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    expect(canvas.getByText("Site visit")).toBeInTheDocument();
    expect(canvas.getByText("Archived")).toBeInTheDocument();
  },
};

export const Empty: Story = {
  name: "no types yet",
  beforeEach: fakeActivityTypesBackend({ types: [] }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("No activity types")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeActivityTypesBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Couldn't load activity types.")).toBeInTheDocument());
  },
};

export const AtLimit: Story = {
  name: "at the 50-type limit",
  beforeEach: fakeActivityTypesBackend({
    types: Array.from({ length: 50 }, (_, i) => ({
      key: `type_${i}`,
      label: { en: `Type ${i}` },
      icon: "circle",
      usageCount: 0,
    })),
  }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    expect(canvas.getByText("A workspace can have at most 50 activity types.")).toBeInTheDocument();
    expect(canvas.getByRole("button", { name: "Add type" })).toBeDisabled();
  },
};
