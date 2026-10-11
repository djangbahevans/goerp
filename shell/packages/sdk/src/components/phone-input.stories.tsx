import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, screen, userEvent, within } from "storybook/test";
import { FieldWrapper } from "./field-wrapper.js";
import { PhoneInput } from "./phone-input.js";

const meta: Meta<typeof PhoneInput> = {
  title: "Form Fields/PhoneInput",
  component: PhoneInput,
  args: { value: "", onChange: () => {} },
  render: function Example(args) {
    const [value, setValue] = useState(args.value);
    return (
      <FieldWrapper label="Phone">
        <PhoneInput
          {...args}
          value={value}
          onChange={(next) => {
            setValue(next);
            args.onChange(next);
          }}
        />
        <output aria-label="Stored value">{value}</output>
      </FieldWrapper>
    );
  },
};
export default meta;
type Story = StoryObj<typeof PhoneInput>;

export const Empty: Story = {};
export const Prefilled: Story = { args: { value: "+233 50-123-4567" } };
export const National: Story = { args: { value: "0501234567" } };
export const SharedCallingCode: Story = { args: { value: "+1 202 555 0123" } };
export const Disabled: Story = { args: { value: "+233 501234567", disabled: true } };
export const Validation: Story = {
  render: function Example(args) {
    const [value, setValue] = useState("050");
    return (
      <FieldWrapper
        label="Phone"
        description="Include the subscriber number."
        error="Enter a complete phone number."
        required
      >
        <PhoneInput {...args} value={value} onChange={setValue} />
      </FieldWrapper>
    );
  },
};
export const Narrow: Story = {
  args: { value: "+233 501234567" },
  decorators: [
    (Story) => (
      <div style={{ width: 260, maxWidth: "100%" }}>
        <Story />
      </div>
    ),
  ],
};
export const Keyboard: Story = {
  args: { value: "0501234567" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const country = canvas.getByRole("combobox", { name: "Country calling code" });
    await userEvent.tab();
    await expect(country).toHaveFocus();
    await userEvent.type(country, "Ghana");
    await expect(screen.getByRole("option", { name: "Ghana (+233)" })).toBeVisible();
    await userEvent.keyboard("{Enter}");
    await expect(canvas.getByLabelText("Stored value")).toHaveTextContent("+233 0501234567");
    await expect(country).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}{Escape}");
    await expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await userEvent.tab();
    const phone = canvas.getByRole("textbox", { name: "Phone" });
    await expect(phone).toHaveFocus();
    await userEvent.clear(phone);
    await expect(canvas.getByLabelText("Stored value")).toHaveTextContent(/^$/);
  },
};
