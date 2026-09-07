import type { Meta, StoryObj } from "@storybook/react-vite";
import { AlertDialog } from "./alert-dialog.js";

const meta: Meta<typeof AlertDialog> = {
  title: "Actions/AlertDialog",
  component: AlertDialog,
  args: {
    open: true,
    onConfirm: () => {},
    onCancel: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof AlertDialog>;

export const Default: Story = {
  args: {
    title: "Archive Contact",
    description: "Archiving removes this contact from active lists. This can be undone later.",
  },
};

export const Destructive: Story = {
  args: {
    title: "Delete Contact",
    description: "This cannot be undone.",
    confirmLabel: "Delete",
    confirmVariant: "danger",
  },
};

export const WithInput: Story = {
  args: {
    title: "Cancel Order",
    description: "Please provide a reason for cancelling this order.",
    input: {
      label: "Reason for cancellation",
      type: "text",
      required: true,
      placeholder: "e.g. Customer requested cancellation",
    },
  },
};
