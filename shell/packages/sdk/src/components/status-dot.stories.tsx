import type { Meta, StoryObj } from "@storybook/react-vite";
import { StatusDot } from "./status-dot.js";

const meta: Meta<typeof StatusDot> = {
  title: "Badges & Indicators/StatusDot",
  component: StatusDot,
};

export default meta;

type Story = StoryObj<typeof StatusDot>;

export const Default: Story = {
  args: {
    color: "green",
    label: "Active",
  },
};

export const Pulsing: Story = {
  args: {
    color: "red",
    label: "Overdue",
    pulse: true,
  },
};

export const WithoutLabel: Story = {
  args: {
    color: "orange",
  },
};
