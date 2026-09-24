import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { TextLink } from "./text-link.js";

const meta: Meta<typeof TextLink> = {
  title: "Navigation/TextLink",
  component: TextLink,
  args: { href: "#", children: "Forgot password?" },
  decorators: [
    (Story) => (
      <div className="text-sm text-text">
        <Story />
      </div>
    ),
  ],
};

export default meta;

type Story = StoryObj<typeof TextLink>;

export const Standalone: Story = {};

export const StandaloneHover: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByRole("link"));
  },
};

export const Inline: Story = {
  render: () => (
    <p className="max-w-80">
      Your workspace follows the{" "}
      <TextLink href="#" inline>
        data retention policy
      </TextLink>{" "}
      your administrator set, which keeps deleted records for 30 days before removing them permanently.
    </p>
  ),
};

export const InlineHover: Story = {
  render: () => (
    <p>
      I agree to the{" "}
      <TextLink href="#" inline>
        terms of service
      </TextLink>
    </p>
  ),
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByRole("link"));
  },
};

export const External: Story = {
  render: () => (
    <p>
      I agree to the{" "}
      <TextLink href="https://example.com/terms" inline external>
        terms of service
      </TextLink>
    </p>
  ),
};

export const FocusVisible: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.tab();
    await expect(within(canvasElement).getByRole("link")).toHaveFocus();
  },
};
