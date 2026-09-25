import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { AdminCreateRolePage, AdminRoleDetailPage } from "./admin-role-detail-page.js";
import { fakeRolesBackend } from "./admin-roles-story-fixtures.js";

const meta: Meta<typeof AdminRoleDetailPage> = {
  title: "Admin/Roles/Detail",
  component: AdminRoleDetailPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { roleId: "r-sales-rep", onBackToList: () => {}, onOpenUsers: () => {} },
};

export default meta;

type Story = StoryObj<typeof AdminRoleDetailPage>;

export const CustomRole: Story = {
  name: "custom role with users",
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("link", { name: "2 users" })).toBeInTheDocument());
    expect(canvas.getByRole("button", { name: "Delete role" })).toBeDisabled();
    expect(canvas.getByRole("button", { name: "Save changes" })).toBeDisabled();
  },
};

export const UnsavedChanges: Story = {
  name: "unsaved matrix change",
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("checkbox", { name: "contacts:contact:write" }));
    expect(canvas.getByRole("button", { name: "Save changes" })).toBeEnabled();
    expect(canvas.getByRole("button", { name: "Discard changes" })).toBeInTheDocument();
  },
};

export const ReadDependency: Story = {
  name: "deselecting read clears its group",
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("checkbox", { name: "sales:quote:read" }));
    expect(canvas.getByRole("checkbox", { name: "sales:quote:write" })).not.toBeChecked();
    expect(canvas.getByRole("checkbox", { name: "sales:order:write" })).toBeChecked();
  },
};

export const DeletableRole: Story = {
  name: "role without users, uncatalogued permission",
  args: { roleId: "r-auditor" },
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("group", { name: "Other permissions" })).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("button", { name: "Delete role" }));
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();
  },
};

export const BuiltInRole: Story = {
  name: "built-in role, read-only",
  args: { roleId: "r-user" },
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText(/is a built-in role/)).toBeInTheDocument());
    expect(canvas.queryByRole("button", { name: "Save changes" })).toBeNull();
    expect(canvas.queryByRole("button", { name: "Delete role" })).toBeNull();
  },
};

export const NotFound: Story = {
  name: "not found",
  args: { roleId: "missing" },
  beforeEach: fakeRolesBackend(),
};

export const Create: StoryObj<typeof AdminCreateRolePage> = {
  name: "create role",
  render: () => <AdminCreateRolePage onCreated={() => {}} />,
  beforeEach: fakeRolesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByRole("textbox", { name: "Name" }), "Bad Name");
    await userEvent.click(canvas.getByRole("button", { name: "Create role" }));
    expect(await canvas.findByText(/starting with a letter/)).toBeInTheDocument();
  },
};
