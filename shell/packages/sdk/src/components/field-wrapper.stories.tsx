import type { Meta, StoryObj } from "@storybook/react-vite";
import { FieldWrapper } from "./field-wrapper.js";
import { SegmentedField } from "./segmented-field.js";

const meta: Meta<typeof FieldWrapper> = {
  title: "Form Fields/FieldWrapper",
  component: FieldWrapper,
};

export default meta;

type Story = StoryObj<typeof FieldWrapper>;

export const Default: Story = {
  args: {
    label: "Name",
    required: true,
    children: <input />,
  },
};

export const WithError: Story = {
  args: {
    label: "Email",
    children: <input type="email" />,
    error: "Invalid email address",
  },
};

export const WrappingASegmentedField: Story = {
  render: () => (
    <FieldWrapper label="Type">
      <SegmentedField
        options={[
          { value: "person", label: "Person" },
          { value: "company", label: "Company" },
        ]}
        value="person"
        onChange={() => {}}
      />
    </FieldWrapper>
  ),
};
