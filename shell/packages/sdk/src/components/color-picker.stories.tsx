import type { Meta, StoryObj } from "@storybook/react-vite";
import { ColorPicker } from "./color-picker.js";

const meta: Meta<typeof ColorPicker> = {
  title: "Form Fields/ColorPicker",
  component: ColorPicker,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof ColorPicker>;

export const Default: Story = {
  args: {
    label: "Brand color",
    value: "#2563eb",
  },
};

export const WithError: Story = {
  args: {
    label: "Brand color",
    value: "#2563eb",
    error: "Choose a color with sufficient contrast",
  },
};

export const Disabled: Story = {
  args: {
    label: "Brand color",
    value: "#2563eb",
    disabled: true,
  },
};
