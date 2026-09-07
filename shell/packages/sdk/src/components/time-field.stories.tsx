import type { Meta, StoryObj } from "@storybook/react-vite";
import { TimeField } from "./date-fields.js";

const meta: Meta<typeof TimeField> = {
  title: "Form Fields/TimeField",
  component: TimeField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof TimeField>;

export const Default: Story = {
  args: {
    label: "Alarm",
    value: "09:00",
    min: "08:00",
    max: "18:00",
  },
};
