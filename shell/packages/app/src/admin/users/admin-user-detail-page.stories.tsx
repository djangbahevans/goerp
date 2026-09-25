import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { AdminUserDetailPage } from "./admin-user-detail-page.js";
import { fakeBackend, withAdminShell } from "./admin-users-story-fixtures.js";

const meta: Meta<typeof AdminUserDetailPage> = {
  title: "Admin/Users/Detail",
  component: AdminUserDetailPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { userId: "u-bola", onBackToList: () => {} },
};

export default meta;

type Story = StoryObj<typeof AdminUserDetailPage>;

export const ActiveMember: Story = {
  name: "active member with sessions",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Safari on macOS")).toBeInTheDocument());
    expect(canvas.getByText("Chrome on Android")).toBeInTheDocument();
  },
};

export const Suspended: Story = {
  name: "suspended member",
  args: { userId: "u-chidi" },
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    await waitFor(() =>
      expect(within(canvasElement).getByRole("button", { name: "Unsuspend user" })).toBeInTheDocument(),
    );
  },
};

export const Invitee: Story = {
  name: "pending invitee",
  args: { userId: "u-efua" },
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    await waitFor(() =>
      expect(within(canvasElement).getByRole("button", { name: "Resend invite" })).toBeInTheDocument(),
    );
  },
};

export const OwnAccount: Story = {
  name: "the admin's own account",
  args: { userId: "me" },
  beforeEach: fakeBackend(),
};

export const SuspendDialog: Story = {
  name: "suspend confirmation",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Suspend user" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/every organisation they belong to/)).toBeInTheDocument();
  },
};

export const DeleteDialog: Story = {
  name: "delete confirmation, typed email",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Delete user" }));
    const dialog = await screen.findByRole("alertdialog");
    const confirm = within(dialog).getByRole("button", { name: "Delete user" });
    expect(confirm).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox"), "bola.mensah@acme.test");
    expect(confirm).toBeEnabled();
  },
};

export const NotFound: Story = {
  name: "not found",
  args: { userId: "missing" },
  beforeEach: fakeBackend(),
};
