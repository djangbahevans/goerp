import type { Meta, StoryObj } from "@storybook/react-vite";
import { ActionButton } from "./action-button.js";
import { EmptyState } from "./empty-state.js";

const meta: Meta<typeof EmptyState> = {
  title: "Feedback/EmptyState",
  component: EmptyState,
};

export default meta;

type Story = StoryObj<typeof EmptyState>;

export const Default: Story = {
  args: {
    title: "No contacts",
  },
};

export const WithAction: Story = {
  args: {
    icon: "users",
    title: "No orders yet",
    description: "Orders you create will show up here.",
    action: (
      <ActionButton variant="primary" onClick={() => {}}>
        New Order
      </ActionButton>
    ),
  },
};
