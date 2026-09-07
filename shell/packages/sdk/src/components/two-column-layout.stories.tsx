import type { Meta, StoryObj } from "@storybook/react-vite";
import { TwoColumnLayout } from "./two-column-layout.js";

const meta: Meta<typeof TwoColumnLayout> = {
  title: "Layout/TwoColumnLayout",
  component: TwoColumnLayout,
};

export default meta;

type Story = StoryObj<typeof TwoColumnLayout>;

export const Default: Story = {
  args: {
    children: <p>Main content</p>,
    sidebar: <p>Sidebar content</p>,
  },
};

export const WithoutSidebar: Story = {
  args: {
    children: <p>Main content, full width</p>,
    sidebar: undefined,
  },
};
