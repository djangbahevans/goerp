import type { Meta, StoryObj } from "@storybook/react-vite";
import { DateTimeField } from "./date-fields.js";

const meta: Meta<typeof DateTimeField> = {
  title: "Form Fields/DateTimeField",
  component: DateTimeField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof DateTimeField>;

export const Default: Story = {
  args: {
    label: "Starts",
    value: new Date(),
  },
};

export const WithError: Story = {
  args: {
    label: "Starts",
    value: undefined,
    error: "Start time is required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Starts",
    value: new Date(),
    disabled: true,
  },
};
