import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, waitFor, within } from "storybook/test";
import type { AdminUserStatusFilter } from "./admin-users-api.js";
import { AdminUsersPage } from "./admin-users-page.js";
import { fakeBackend, STORY_USERS, withAdminShell } from "./admin-users-story-fixtures.js";
import type { FakeUser } from "./fake-admin-users-backend.js";

// The route owns search and status in the URL; here local state stands in.
function Harness() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<AdminUserStatusFilter>("all");
  return (
    <AdminUsersPage
      search={search}
      status={status}
      onSearchChange={setSearch}
      onStatusChange={setStatus}
      onOpenUser={() => {}}
    />
  );
}

const meta: Meta<typeof Harness> = {
  title: "Admin/Users/List",
  component: Harness,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof Harness>;

export const Default: Story = {
  name: "members and invitees",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    expect(canvas.getByText("Showing 5 of 5")).toBeInTheDocument();
  },
};

export const InvitedTab: Story = {
  name: "Invited tab",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("table")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("tab", { name: "Invited" }));
    await waitFor(() => expect(canvas.getByText("Showing 1 of 1")).toBeInTheDocument());
    expect(canvas.getByText("efua.boateng@acme.test")).toBeInTheDocument();
  },
};

const many: FakeUser[] = Array.from({ length: 70 }, (_, i) => ({
  id: `bulk-${i}`,
  name: `Team Member ${i + 1}`,
  email: `member${String(i + 1).padStart(2, "0")}@acme.test`,
  roles: ["user"],
  status: "active",
  lastLoginAt: null,
}));

export const LargeTenant: Story = {
  name: "large tenant with Load more",
  beforeEach: fakeBackend({ users: [...STORY_USERS, ...many] }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Showing 50 of 75")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(canvas.getByText("Showing 75 of 75")).toBeInTheDocument());
  },
};

export const NoMatch: Story = {
  name: "search with no match",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(canvas.getByRole("searchbox", { name: "Search users" }), "zzz");
    await waitFor(() => expect(canvas.getByText("No users found")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    const alert = await waitFor(() => within(canvasElement).getByRole("alert"));
    expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};
