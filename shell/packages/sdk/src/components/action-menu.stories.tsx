import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { ActionMenu } from "./action-menu.js";

const meta: Meta<typeof ActionMenu> = {
  title: "Actions/ActionMenu",
  component: ActionMenu,
};

export default meta;

type Story = StoryObj<typeof ActionMenu>;

// Opens on load (rather than requiring a manual click in Storybook's UI) so
// the panel — including the danger item, separator, and disabled item — is
// visible in the sidebar preview per the doc's "Open" state.
export const Default: Story = {
  args: {
    label: "Actions",
    items: [
      { label: "Edit", icon: "edit", onClick: () => {} },
      { label: "Duplicate", icon: "copy", onClick: () => {} },
      { label: "Export", icon: "download", onClick: () => {}, disabled: true },
      { type: "separator" },
      { label: "Archive", icon: "archive", onClick: () => {}, variant: "danger" },
    ],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Actions" }));
    await expect(canvas.getByRole("menu")).toBeInTheDocument();
  },
};

export const Closed: Story = {
  args: {
    label: "Actions",
    items: [
      { label: "Edit", icon: "edit", onClick: () => {} },
      { label: "Archive", icon: "archive", onClick: () => {}, variant: "danger" },
    ],
  },
};
