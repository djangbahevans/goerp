import type { Meta, StoryObj } from "@storybook/react-vite";
import { PasswordStrengthMeter } from "./password-strength-meter.js";

const meta: Meta<typeof PasswordStrengthMeter> = {
  title: "Form Fields/PasswordStrengthMeter",
  component: PasswordStrengthMeter,
};

export default meta;

type Story = StoryObj<typeof PasswordStrengthMeter>;

export const Empty: Story = {
  args: {
    password: "",
  },
};

// Repeated characters keep zxcvbn's score at 0 regardless of length.
export const ScoreWeak: Story = {
  args: {
    password: "aaaaaaaaaaaa",
  },
};

export const ScoreFair: Story = {
  args: {
    password: "sunshine2024!",
  },
};

export const ScoreGood: Story = {
  args: {
    password: "P@ssw0rd12345678901234",
  },
};

export const ScoreStrong: Story = {
  args: {
    password: "correcthorsebatterystaple",
  },
};

export const BelowLengthFloor: Story = {
  args: {
    password: "X7$qzM!2",
    minLength: 12,
  },
};
