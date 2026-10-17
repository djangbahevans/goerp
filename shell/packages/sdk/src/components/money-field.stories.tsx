import type { Meta, StoryObj } from "@storybook/react-vite";
import { MoneyField } from "./money-field.js";

const meta: Meta<typeof MoneyField> = {
  title: "Form Fields/MoneyField",
  component: MoneyField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof MoneyField>;

export const Default: Story = {
  args: {
    label: "Amount",
    value: 100,
    currency: "GHS",
  },
};

export const WithError: Story = {
  args: {
    label: "Amount",
    value: 0,
    currency: "GHS",
    error: "Amount must be greater than zero",
  },
};

export const Disabled: Story = {
  // color-contrast: WCAG 1.4.3 exempts an inactive control; axe checks the disabled field's
  // currency adornment as if it were ordinary text.
  parameters: { a11y: { config: { rules: [{ id: "color-contrast", enabled: false }] } } },
  args: {
    label: "Amount",
    value: 100,
    currency: "GHS",
    disabled: true,
  },
};
