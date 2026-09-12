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
    // The panel portals to document.body, not canvasElement.
    await expect(within(document.body).getByRole("menu")).toBeInTheDocument();
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

// chrome-header.md's UserMenu: a checked item (theme toggle) and a custom
// avatar-shaped trigger instead of the default labeled button.
export const CheckedItemAndCustomTrigger: Story = {
  args: {
    label: "Account",
    items: [
      { label: "Light mode", checked: false, onClick: () => {} },
      { label: "Dark mode", checked: true, onClick: () => {} },
      { type: "separator" },
      { label: "Sign out", onClick: () => {} },
    ],
    trigger: ({ ref, open, onClick, onKeyDown }) => (
      <button
        ref={ref}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
        onClick={onClick}
        onKeyDown={onKeyDown}
        className="rounded-full border border-border bg-surface px-3 py-1.5 text-sm text-text hover:bg-surface-hover"
      >
        JD
      </button>
    ),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Account menu" }));
    // The panel portals to document.body, not canvasElement.
    const darkMode = within(document.body).getByRole("menuitemcheckbox", { name: "Dark mode" });
    await expect(darkMode).toHaveAttribute("aria-checked", "true");

    // Clicking a checked item toggles it but leaves the menu open — unlike a
    // plain item, which closes on click — so a checklist of several checked
    // items (list-renderer.md's Columns toggle) can be flipped in one open.
    await userEvent.click(darkMode);
    await expect(within(document.body).getByRole("menu")).toBeInTheDocument();
  },
};
