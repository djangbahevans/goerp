import type { Meta, StoryObj } from "@storybook/react-vite";
import { Countdown } from "./countdown.js";

const meta: Meta<typeof Countdown> = {
  title: "Feedback/Countdown",
  component: Countdown,
};

export default meta;

type Story = StoryObj<typeof Countdown>;

// Login lockout copy: "Too many attempts. Try again in {N} seconds."
export const LoginLockout: Story = {
  render: (args) => (
    <p>
      Too many attempts. Try again in <Countdown {...args} />.
    </p>
  ),
  args: {
    seconds: 60,
  },
};

// Resend-code cooldown: a shorter "0:45" mm:ss-style format.
export const ResendCodeCooldown: Story = {
  render: (args) => (
    <p>
      Resend code in <Countdown {...args} />
    </p>
  ),
  args: {
    seconds: 45,
    format: (secondsRemaining: number) => `0:${String(secondsRemaining).padStart(2, "0")}`,
  },
};
