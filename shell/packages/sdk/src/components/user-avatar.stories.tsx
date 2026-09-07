import type { Meta, StoryObj } from "@storybook/react-vite";
import { UserAvatar } from "./user-avatar.js";

const meta: Meta<typeof UserAvatar> = {
  title: "Badges & Indicators/UserAvatar",
  component: UserAvatar,
};

export default meta;

type Story = StoryObj<typeof UserAvatar>;

export const Initials: Story = {
  args: {
    name: "Ama Boateng",
  },
};

export const WithTooltip: Story = {
  args: {
    name: "Kwame Mensah",
    showTooltip: true,
  },
};

export const Sizes: Story = {
  render: () => (
    <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
      <UserAvatar name="Ama Boateng" size="xs" />
      <UserAvatar name="Ama Boateng" size="sm" />
      <UserAvatar name="Ama Boateng" size="md" />
      <UserAvatar name="Ama Boateng" size="lg" />
    </div>
  ),
};
