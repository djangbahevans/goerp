import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { SliderField } from "./slider-field.js";

const meta: Meta<typeof SliderField> = {
  title: "Form Fields/SliderField",
  component: SliderField,
  args: {
    min: 0,
    max: 100,
    step: 1,
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof SliderField>;

export const Low: Story = {
  args: {
    value: 10,
  },
};

export const Mid: Story = {
  args: {
    value: 50,
  },
};

export const High: Story = {
  args: {
    value: 90,
  },
};

export const Focus: Story = {
  args: {
    value: 50,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.tab();
    await expect(canvas.getByRole("slider")).toHaveFocus();
  },
};

export const Disabled: Story = {
  args: {
    value: 50,
    disabled: true,
  },
};
