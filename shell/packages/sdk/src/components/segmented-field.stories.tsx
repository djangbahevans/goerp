import type { Meta, StoryObj } from "@storybook/react-vite";
import { SegmentedField } from "./segmented-field.js";

const meta: Meta<typeof SegmentedField> = {
  title: "Form Fields/SegmentedField",
  component: SegmentedField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof SegmentedField>;

export const Default: Story = {
  args: {
    options: [
      { value: "person", label: "Person" },
      { value: "company", label: "Company" },
    ],
    value: "person",
  },
};

export const WithDisabledOption: Story = {
  args: {
    options: [
      { value: "active", label: "Active" },
      { value: "archived", label: "Archived", disabled: true },
    ],
    value: "active",
  },
};
