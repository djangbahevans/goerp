import { Button } from "@goerp/sdk/components";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { ErrorLayout } from "./error-layout.js";

const meta = {
  title: "Shell/Errors/ErrorLayout",
  component: ErrorLayout,
  parameters: { layout: "fullscreen" },
  args: {
    icon: "compass",
    heading: "Page not found",
    description: "This page doesn't exist or has been moved.",
    actions: (
      <>
        <Button variant="primary">Go home</Button>
        <Button>Go back</Button>
      </>
    ),
  },
} satisfies Meta<typeof ErrorLayout>;

export default meta;
type Story = StoryObj<typeof meta>;

export const TwoActions: Story = {};

export const OneAction: Story = {
  args: { actions: <Button variant="primary">Go home</Button> },
};

export const WithFootnote: Story = {
  args: { icon: "unplug", footnote: "Error ref: 4bf92f3577b34da6a3ce929d0e0e4736" },
};
