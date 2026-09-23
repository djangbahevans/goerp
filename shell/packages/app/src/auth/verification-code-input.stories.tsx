import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, fn, userEvent, within } from "storybook/test";
import { AuthLayout } from "./auth-layout.js";
import { VerificationCodeInput, type VerificationCodeInputProps } from "./verification-code-input.js";

const DESCRIPTION = "Enter the 6-digit code from your authenticator app.";

// Controlled wrapper so typing and paste work in the canvas; each story seeds
// the initial value through args.
function Controlled(args: VerificationCodeInputProps) {
  const [value, setValue] = useState(args.value);
  return (
    <AuthLayout>
      <h1 className="mb-6 font-semibold text-text text-xl">Enter your verification code</h1>
      <VerificationCodeInput {...args} value={value} onChange={setValue} />
    </AuthLayout>
  );
}

const meta = {
  title: "Shell/Auth/VerificationCodeInput",
  component: VerificationCodeInput,
  render: (args) => <Controlled {...args} />,
  args: {
    label: "Verification code",
    description: DESCRIPTION,
    value: "",
    onChange: fn(),
    onComplete: fn(),
  },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof VerificationCodeInput>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {};

export const Partial: Story = { args: { value: "123" } };

export const Complete: Story = {
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.type(canvas.getByLabelText("Verification code"), "123456");
    await expect(args.onComplete).toHaveBeenCalledWith("123456");
  },
};

export const PastedWithSeparator: Story = {
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByLabelText("Verification code"));
    await userEvent.paste("123-456");
    await expect(canvas.getByLabelText("Verification code")).toHaveValue("123456");
    await expect(args.onComplete).toHaveBeenCalledWith("123456");
  },
};

export const Focus: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.tab();
    await expect(canvas.getByLabelText("Verification code")).toHaveFocus();
  },
};

export const ErrorState: Story = {
  name: "Error",
  args: { error: "Couldn't verify the code. Check your connection and try again." },
};

export const Disabled: Story = { args: { value: "123456", disabled: true } };
