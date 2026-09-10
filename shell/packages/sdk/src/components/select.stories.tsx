import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { Select } from "./select.js";

const PLAIN_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "in_progress", label: "In Progress" },
  { value: "done", label: "Done" },
];

const COLOR_OPTIONS = [
  { value: "draft", label: "Draft", color: "gray" },
  { value: "confirmed", label: "Confirmed", color: "green" },
  { value: "overdue", label: "Overdue", color: "red" },
];

const ICON_OPTIONS = [
  { value: "low", label: "Low", icon: "arrow-down" },
  { value: "medium", label: "Medium", icon: "minus" },
  { value: "high", label: "High", icon: "arrow-up" },
];

const DISABLED_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "confirmed", label: "Confirmed" },
  { value: "archived", label: "Archived", disabled: true },
];

const meta: Meta<typeof Select> = {
  title: "Form Fields/Select",
  component: Select,
  args: {
    options: PLAIN_OPTIONS,
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof Select>;

// Opens on load so the panel is visible in the sidebar preview, matching
// relation-picker.stories.tsx's "Default" convention. Single-select's panel
// renders through a Radix Portal to document.body, outside canvasElement —
// option assertions query the document-scoped `screen`, not `canvas`.
export const Plain: Story = {
  args: {
    value: "",
    placeholder: "Select a status…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "Draft" })).toBeInTheDocument());
  },
};

export const WithIcon: Story = {
  args: {
    options: ICON_OPTIONS,
    value: "medium",
  },
};

export const WithColor: Story = {
  args: {
    options: COLOR_OPTIONS,
    value: "confirmed",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "Overdue" })).toBeInTheDocument());
  },
};

export const Multiple: Story = {
  args: {
    options: PLAIN_OPTIONS,
    value: ["draft", "in_progress"],
    multiple: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(canvas.getByRole("option", { name: "Done" })).toBeInTheDocument());
  },
};

export const DisabledOption: Story = {
  args: {
    options: DISABLED_OPTIONS,
    value: "",
    placeholder: "Select a status…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    const archived = await screen.findByRole("option", { name: "Archived" });
    await expect(archived).toHaveAttribute("aria-disabled", "true");
  },
};

export const Disabled: Story = {
  args: {
    options: PLAIN_OPTIONS,
    value: "draft",
    disabled: true,
  },
};
