import type { Meta, StoryObj } from "@storybook/react-vite";
import { ProgressBar } from "./progress-bar.js";

const meta: Meta<typeof ProgressBar> = {
  title: "Feedback/ProgressBar",
  component: ProgressBar,
};

export default meta;

type Story = StoryObj<typeof ProgressBar>;

export const Default: Story = {
  args: {
    value: 75,
  },
};

export const WithLabel: Story = {
  args: {
    value: 75,
    label: "75% complete",
    showLabel: true,
  },
};

export const Complete: Story = {
  args: {
    value: 100,
    label: "100% complete",
    showLabel: true,
  },
};
