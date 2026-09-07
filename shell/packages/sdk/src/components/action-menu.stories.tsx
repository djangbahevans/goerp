import type { Meta, StoryObj } from "@storybook/react-vite";
import { ActionMenu } from "./action-menu.js";

const meta: Meta<typeof ActionMenu> = {
  title: "Actions/ActionMenu",
  component: ActionMenu,
};

export default meta;

type Story = StoryObj<typeof ActionMenu>;

export const Default: Story = {
  args: {
    label: "Actions",
    items: [
      { label: "Edit", icon: "edit", onClick: () => {} },
      { label: "Duplicate", icon: "copy", onClick: () => {} },
      { type: "separator" },
      { label: "Archive", icon: "archive", onClick: () => {}, variant: "danger" },
    ],
  },
};
