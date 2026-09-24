import type { Meta, StoryObj } from "@storybook/react-vite";
import { FieldWrapper } from "./field-wrapper.js";
import { SegmentedField } from "./segmented-field.js";
import { TextInput } from "./text-input.js";

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
    children: <TextInput value="" onChange={() => {}} />,
  },
};

export const WithDescription: Story = {
  args: {
    label: "Display name",
    description: "Shown to other people in your workspace.",
    children: <TextInput value="" onChange={() => {}} />,
  },
};

export const WithError: Story = {
  args: {
    label: "Email",
    children: <TextInput type="email" value="ama@" onChange={() => {}} />,
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
