import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { ToggleField } from "./toggle-field.js";

const meta: Meta<typeof ToggleField> = {
  title: "Form Fields/ToggleField",
  component: ToggleField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof ToggleField>;

export const Off: Story = {
  args: {
    value: false,
  },
};

export const On: Story = {
  args: {
    value: true,
  },
};

export const Focus: Story = {
  args: {
    value: false,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.tab();
    await expect(canvas.getByRole("switch")).toHaveFocus();
  },
};

export const Disabled: Story = {
  args: {
    value: false,
    disabled: true,
  },
};

export const DisabledOn: Story = {
  args: {
    value: true,
    disabled: true,
  },
};
