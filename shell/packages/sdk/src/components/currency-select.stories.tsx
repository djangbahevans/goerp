import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { CurrencySelect } from "./currency-select.js";

const meta: Meta<typeof CurrencySelect> = {
  title: "Form Fields/CurrencySelect",
  component: CurrencySelect,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof CurrencySelect>;

export const Closed: Story = {
  args: {
    value: "",
    placeholder: "Select a currency…",
  },
};

// Opens on load so the panel is visible in the sidebar preview, matching
// country-select.stories.tsx's "Open" convention.
export const Open: Story = {
  args: {
    value: "",
    placeholder: "Select a currency…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "EUR" })).toBeInTheDocument());
  },
};

export const ClosedWithValue: Story = {
  args: {
    value: "USD",
  },
};

export const Filtered: Story = {
  args: {
    value: "",
    placeholder: "Select a currency…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "eur");
    await waitFor(() => expect(screen.getByRole("option", { name: "EUR" })).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: "",
    placeholder: "Select a currency…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "zzznotacurrency");
    await waitFor(() => expect(screen.getByText('No results for "zzznotacurrency"')).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: "USD",
    disabled: true,
  },
};
