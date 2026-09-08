import type { Meta, StoryObj } from "@storybook/react-vite";
import { CodeField } from "./code-field.js";

const meta: Meta<typeof CodeField> = {
  title: "Form Fields/CodeField",
  component: CodeField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof CodeField>;

export const Default: Story = {
  args: {
    label: "Handler",
    value: "function onWebhook(event) {\n  return event.payload;\n}",
    language: "javascript",
  },
};

export const WithError: Story = {
  args: {
    label: "Handler",
    value: "",
    error: "Handler code is required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Handler",
    value: "function onWebhook(event) {\n  return event.payload;\n}",
    language: "javascript",
    disabled: true,
  },
};
