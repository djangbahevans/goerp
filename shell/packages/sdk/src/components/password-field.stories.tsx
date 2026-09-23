import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { PasswordField } from "./password-field.js";

const meta: Meta<typeof PasswordField> = {
  title: "Form Fields/PasswordField",
  component: PasswordField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof PasswordField>;

export const Default: Story = {
  args: {
    label: "Password",
    value: "hunter2",
    autoComplete: "current-password",
  },
};

// Toggles visibility on load so the revealed state is visible in the
// sidebar preview, matching currency-select.stories.tsx's "Open" convention.
export const Revealed: Story = {
  args: {
    label: "Password",
    value: "hunter2",
    autoComplete: "current-password",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Show password" }));
  },
};

export const Focus: Story = {
  args: {
    label: "Password",
    value: "hunter2",
    autoComplete: "current-password",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.tab();
    await expect(canvas.getByLabelText("Password")).toHaveFocus();
  },
};

export const WithError: Story = {
  args: {
    label: "Password",
    value: "",
    autoComplete: "current-password",
    error: "Invalid email or password",
  },
};

export const Disabled: Story = {
  args: {
    label: "Password",
    value: "hunter2",
    autoComplete: "current-password",
    disabled: true,
  },
};
