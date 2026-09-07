import type { Meta, StoryObj } from "@storybook/react-vite";
import { Skeleton } from "./skeleton.js";

const meta: Meta<typeof Skeleton> = {
  title: "Feedback/Skeleton",
  component: Skeleton,
};

export default meta;

type Story = StoryObj<typeof Skeleton>;

export const Lines: Story = {
  args: {
    lines: 3,
  },
};

export const Card: Story = {
  args: {
    type: "card",
  },
};

export const Table: Story = {
  args: {
    type: "table",
    rows: 3,
    columns: 4,
  },
};
