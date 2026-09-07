import type { Meta, StoryObj } from "@storybook/react-vite";
import { LoadingOverlay } from "./loading-overlay.js";

const meta: Meta<typeof LoadingOverlay> = {
  title: "Feedback/LoadingOverlay",
  component: LoadingOverlay,
};

export default meta;

type Story = StoryObj<typeof LoadingOverlay>;

export const Default: Story = {
  render: (args) => (
    <div style={{ position: "relative", border: "1px solid #ccc", padding: "2rem" }}>
      <p>Name: Ama Boateng</p>
      <p>Email: ama@example.com</p>
      <LoadingOverlay {...args} />
    </div>
  ),
};

export const CustomLabel: Story = {
  ...Default,
  args: {
    label: "Saving…",
  },
};
