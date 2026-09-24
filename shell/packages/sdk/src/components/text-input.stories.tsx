import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { Button } from "./button.js";
import { FieldWrapper } from "./field-wrapper.js";
import { Icon } from "./icon.js";
import { IconButton } from "./icon-button.js";
import { TextArea, type TextAreaProps, TextInput, type TextInputProps } from "./text-input.js";

function ControlledTextInput(props: Omit<TextInputProps, "value" | "onChange"> & { initialValue?: string }) {
  const { initialValue = "", ...rest } = props;
  const [value, setValue] = useState(initialValue);
  return <TextInput {...rest} value={value} onChange={setValue} />;
}

function ControlledTextArea(props: Omit<TextAreaProps, "value" | "onChange"> & { initialValue?: string }) {
  const { initialValue = "", ...rest } = props;
  const [value, setValue] = useState(initialValue);
  return <TextArea {...rest} value={value} onChange={setValue} />;
}

const meta: Meta<typeof ControlledTextInput> = {
  title: "Form Fields/TextInput",
  component: ControlledTextInput,
  args: { "aria-label": "Name" },
  decorators: [
    (Story) => (
      <div className="w-80">
        <Story />
      </div>
    ),
  ],
};

export default meta;

type Story = StoryObj<typeof ControlledTextInput>;

export const Default: Story = {};

export const Hover: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByRole("textbox"));
  },
};

export const Focus: Story = {
  args: { initialValue: "Ama Mensah" },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("textbox");
    await userEvent.click(input);
    await expect(input).toHaveFocus();
  },
};

export const Invalid: Story = { args: { initialValue: "ama@", invalid: true } };

export const Disabled: Story = { args: { initialValue: "Ama Mensah", disabled: true } };

export const ReadOnly: Story = { args: { initialValue: "ama@example.com", readOnly: true } };

export const Placeholder: Story = { args: { "aria-label": "Email", type: "email", placeholder: "name@company.com" } };

export const WithStartAndEnd: Story = {
  render: function Render() {
    const [value, setValue] = useState("Accra");
    return (
      <TextInput
        aria-label="Search"
        type="search"
        value={value}
        onChange={setValue}
        start={<Icon name="search" size={16} aria-hidden="true" />}
        end={value ? <IconButton icon="x" label="Clear search" size="sm" onClick={() => setValue("")} /> : undefined}
      />
    );
  },
};

export const Sizes: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      {(["md", "sm"] as const).map((size) => (
        <div key={size} className="flex items-center gap-2">
          <ControlledTextInput aria-label={`Filter ${size}`} size={size} placeholder={`Filter (${size})`} />
          <Button size={size} variant="primary">
            Apply
          </Button>
        </div>
      ))}
    </div>
  ),
};

export const InFieldWrapper: Story = {
  render: () => (
    <div className="flex flex-col gap-4">
      <FieldWrapper label="Email" description="We'll send the sign-in link here." required>
        <ControlledTextInput type="email" placeholder="name@company.com" />
      </FieldWrapper>
      <FieldWrapper
        label="Email"
        description="We'll send the sign-in link here."
        error="Enter an email address like name@company.com."
        required
      >
        <ControlledTextInput type="email" initialValue="ama@" />
      </FieldWrapper>
    </div>
  ),
};

export const TextAreaDefault: Story = {
  render: () => (
    <FieldWrapper label="Notes" description="Visible to everyone on this record.">
      <ControlledTextArea placeholder="Add a note…" />
    </FieldWrapper>
  ),
};

export const TextAreaStates: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      <ControlledTextArea aria-label="Invalid notes" initialValue="Too long" invalid />
      <ControlledTextArea aria-label="Disabled notes" initialValue="Locked" disabled />
      <ControlledTextArea aria-label="Read-only notes" initialValue="Read only" readOnly resize="none" />
    </div>
  ),
};
