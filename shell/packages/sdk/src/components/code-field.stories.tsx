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

export const PythonHighlighting: Story = {
  args: {
    label: "Handler",
    value: 'def on_webhook(event):\n    # Forward the payload untouched\n    return event["payload"]',
    language: "python",
  },
};

export const UnknownLanguage: Story = {
  args: {
    label: "Handler",
    value: "This renders as plain text: no @codemirror/language-data entry matches.",
    language: "not-a-real-language",
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
