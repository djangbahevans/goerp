import type { Meta, StoryObj } from "@storybook/react-vite";
import { ActionButton } from "./action-button.js";

const meta: Meta<typeof ActionButton> = {
  title: "Actions/ActionButton",
  component: ActionButton,
  args: {
    onClick: () => {},
    children: "Confirm Order",
  },
};

export default meta;

type Story = StoryObj<typeof ActionButton>;

export const Primary: Story = {
  args: {
    variant: "primary",
    icon: "check",
  },
};

export const Secondary: Story = {
  args: {
    variant: "secondary",
  },
};

export const Ghost: Story = {
  args: {
    variant: "ghost",
    size: "sm",
  },
};

export const Danger: Story = {
  args: {
    variant: "danger",
    children: "Archive",
  },
};

export const Loading: Story = {
  args: {
    variant: "primary",
    loading: true,
  },
};
