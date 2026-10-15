import type { Meta, StoryObj } from "@storybook/react-vite";
import { TagPill } from "./tag-pill.js";

const meta: Meta<typeof TagPill> = {
  title: "Badges & Indicators/TagPill",
  component: TagPill,
};

export default meta;

type Story = StoryObj<typeof TagPill>;

export const Default: Story = {
  args: { name: "Customer", color: "#3B82F6" },
};

export const NoColor: Story = {
  args: { name: "Untagged" },
};

const COLORS = ["#3B82F6", "#F59E0B", "#10B981", "#1E3A8A", "#FDE68A", "#FFFFFF", "#000000"];

export const ArbitraryColors: Story = {
  render: () => (
    <div className="flex flex-wrap gap-2">
      {COLORS.map((color) => (
        <TagPill key={color} name={color} color={color} />
      ))}
    </div>
  ),
};
