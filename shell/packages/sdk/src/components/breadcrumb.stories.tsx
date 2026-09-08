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

// A middle item with no onClick renders as plain, non-interactive text —
// still text-text-secondary like a clickable non-current item, but without
// the hover treatment, distinct from both the clickable items and the
// text-text current (last) item.
export const WithNonInteractiveMiddleItem: Story = {
  args: {
    items: [{ label: "Documents", onClick: () => {} }, { label: "2026" }, { label: "Invoices" }],
  },
};
