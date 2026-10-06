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
    await waitFor(() => expect(canvas.getByText("Invoice updated")).toBeInTheDocument());
  },
};

export const ActivityRecordsFilter: Story = {
  name: "activity, records filter",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("radio", { name: "Records" }));
    await waitFor(() => expect(canvas.queryByText("Signed in")).not.toBeInTheDocument());
    expect(canvas.getByText("Changed due_date, notes, status")).toBeInTheDocument();
  },
};

export const ActivityEmpty: Story = {
  name: "activity, empty",
  args: { userId: "u-chidi" },
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("No activity yet")).toBeInTheDocument());
  },
};

export const ActivityError: Story = {
  name: "activity, failed to load",
  beforeEach: fakeBackend({ failActivity: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Couldn't load activity.")).toBeInTheDocument());
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

export const PlatformSuspended: Story = {
  name: "suspended by the platform",
  args: { userId: "u-dayo" },
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Suspended by GoERP")).toBeInTheDocument());
    expect(canvas.getByText("Active")).toBeInTheDocument();
  },
};

export const SuspendDialog: Story = {
  name: "suspend confirmation",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Suspend user" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/Other organisations they belong to aren't affected/)).toBeInTheDocument();
  },
};

export const RemoveDialog: Story = {
  name: "remove from organisation, typed email",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Remove from Acme" }));
    const dialog = await screen.findByRole("alertdialog");
    const confirm = within(dialog).getByRole("button", { name: "Remove from Acme" });
    expect(confirm).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox"), "bola.mensah@acme.test");
    expect(confirm).toBeEnabled();
  },
};

export const ResetTwoFactorDialog: Story = {
  name: "reset two-factor, password confirmation",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Reset two-factor authentication" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/with their password alone/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Reset two-factor" })).toBeDisabled();
    await userEvent.type(within(dialog).getByLabelText("Your password"), "wrong");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset two-factor" }));
    await within(dialog).findByText("Your password is incorrect.");
  },
};

export const NotFound: Story = {
  name: "not found",
  args: { userId: "missing" },
  beforeEach: fakeBackend(),
};
