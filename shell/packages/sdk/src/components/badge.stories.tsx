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

const COLORS: BadgeColor[] = ["gray", "red", "orange", "yellow", "green", "teal", "blue", "indigo", "purple", "pink"];

export const AllColors: Story = {
  render: () => (
    <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
      {COLORS.map((color) => (
        <Badge key={color} label={color} color={color} />
      ))}
    </div>
  ),
};
