import type { Meta, StoryObj } from "@storybook/react-vite";
import { RichTextField } from "./rich-text-field.js";

const meta: Meta<typeof RichTextField> = {
  title: "Form Fields/RichTextField",
  component: RichTextField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof RichTextField>;

export const Default: Story = {
  args: {
    label: "Notes",
    value: "Follow up next week about the renewal.",
  },
};

export const WithError: Story = {
  args: {
    label: "Notes",
    value: "",
    error: "Notes are required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Notes",
    value: "Follow up next week about the renewal.",
    disabled: true,
  },
};
