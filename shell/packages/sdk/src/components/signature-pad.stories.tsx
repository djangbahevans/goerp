import type { Meta, StoryObj } from "@storybook/react-vite";
import { SignaturePad } from "./signature-pad.js";

const meta: Meta<typeof SignaturePad> = {
  title: "Form Fields/SignaturePad",
  component: SignaturePad,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof SignaturePad>;

export const Empty: Story = {
  args: {
    value: null,
  },
};

export const Captured: Story = {
  args: {
    // A 1x1 transparent PNG, standing in for a real captured drawing.
    value:
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  },
};
