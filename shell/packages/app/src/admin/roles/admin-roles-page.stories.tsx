import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { AdminRolesPage } from "./admin-roles-page.js";
import { fakeRolesBackend } from "./admin-roles-story-fixtures.js";

const meta: Meta<typeof AdminRolesPage> = {
  title: "Admin/Roles/List",
  component: AdminRolesPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { onOpenRole: () => {}, onCreateRole: () => {} },
};

export default meta;

type Story = StoryObj<typeof AdminRolesPage>;

export const Default: Story = {
  name: "built-in and custom roles",
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    expect(canvas.getAllByText("System")).toHaveLength(3);
    expect(canvas.getByText("sales_rep")).toBeInTheDocument();
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeRolesBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Couldn't load roles.")).toBeInTheDocument());
  },
};
