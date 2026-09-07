import type { Meta, StoryObj } from "@storybook/react-vite";
import { DateField } from "./date-fields.js";

const meta: Meta<typeof DateField> = {
  title: "Form Fields/DateField",
  component: DateField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof DateField>;

export const Default: Story = {
  args: {
    label: "Due Date",
    value: new Date(),
    min: new Date(),
  },
};

export const Empty: Story = {
  args: {
    label: "Due Date",
    value: undefined,
  },
};
