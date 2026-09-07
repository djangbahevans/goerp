import type { Meta, StoryObj } from "@storybook/react-vite";
import { Breadcrumb } from "./breadcrumb.js";

const meta: Meta<typeof Breadcrumb> = {
  title: "Navigation/Breadcrumb",
  component: Breadcrumb,
};

export default meta;

type Story = StoryObj<typeof Breadcrumb>;

export const Default: Story = {
  args: {
    items: [{ label: "Documents", onClick: () => {} }, { label: "2026", onClick: () => {} }, { label: "Invoices" }],
  },
};
