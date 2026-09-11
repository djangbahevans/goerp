import type { Meta, StoryObj } from "@storybook/react-vite";
import { Badge, type BadgeColor } from "./badge.js";

const meta: Meta<typeof Badge> = {
  title: "Badges & Indicators/Badge",
  component: Badge,
};

export default meta;

type Story = StoryObj<typeof Badge>;

export const Default: Story = {
  args: {
    label: "Confirmed",
    color: "green",
  },
};

export const WithIcon: Story = {
  args: {
    label: "Shipped",
    color: "blue",
    icon: "truck",
  },
};

const COLORS: BadgeColor[] = ["gray", "red", "orange", "yellow", "green", "teal", "blue", "indigo", "purple", "pink"];

export const AllColors: Story = {
  render: () => (
    <div className="flex flex-wrap gap-2">
      {COLORS.map((color) => (
        <Badge key={color} label={color} color={color} />
      ))}
    </div>
  ),
};
