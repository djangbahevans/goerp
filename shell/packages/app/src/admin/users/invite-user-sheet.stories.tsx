import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { fakeBackend, withAdminShell } from "./admin-users-story-fixtures.js";
import { InviteUserSheet } from "./invite-user-sheet.js";

const meta: Meta<typeof InviteUserSheet> = {
  title: "Admin/Users/InviteUserSheet",
  component: InviteUserSheet,
  decorators: [withAdminShell],
  args: { open: true, onClose: () => {} },
};

export default meta;

type Story = StoryObj<typeof InviteUserSheet>;

async function send(email: string) {
  const sheet = await screen.findByRole("dialog", { name: "Invite user" });
  await userEvent.type(within(sheet).getByLabelText(/Email/), email);
  await userEvent.click(within(sheet).getByRole("button", { name: "Send invite" }));
  return sheet;
}

export const Empty: Story = {
  name: "form",
  beforeEach: fakeBackend(),
};

export const Sent: Story = {
  name: "sent to a new person",
  beforeEach: fakeBackend(),
  play: async () => {
    const sheet = await send("yaw.asante@acme.test");
    await waitFor(() => expect(within(sheet).getByText("Invitation sent to yaw.asante@acme.test")).toBeInTheDocument());
  },
};

export const ExistingAccount: Story = {
  name: "sent to someone with a GoERP account",
  beforeEach: fakeBackend({ otherTenantAccounts: ["kofi@partner.test"] }),
  play: async () => {
    const sheet = await send("kofi@partner.test");
    await waitFor(() =>
      expect(within(sheet).getByText(/already has a GoERP account and will be added/)).toBeInTheDocument(),
    );
  },
};

export const AlreadyMember: Story = {
  name: "already a member",
  beforeEach: fakeBackend(),
  play: async () => {
    const sheet = await send("bola.mensah@acme.test");
    await waitFor(() =>
      expect(within(sheet).getByText("This person is already a member of this organisation.")).toBeInTheDocument(),
    );
  },
};
