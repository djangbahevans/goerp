import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { CountrySelect } from "./country-select.js";

const meta: Meta<typeof CountrySelect> = {
  title: "Form Fields/CountrySelect",
  component: CountrySelect,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof CountrySelect>;

export const Closed: Story = {
  args: {
    value: "",
    placeholder: "Select a country…",
  },
};

// Opens on load so the panel is visible in the sidebar preview, matching
// relation-picker.stories.tsx's "Default" convention.
export const Open: Story = {
  args: {
    value: "",
    placeholder: "Select a country…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "Ghana" })).toBeInTheDocument());
  },
};

export const ClosedWithValue: Story = {
  args: {
    value: "GH",
  },
};

export const Filtered: Story = {
  args: {
    value: "",
    placeholder: "Select a country…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "Ghana");
    await waitFor(() => expect(screen.getByRole("option", { name: "Ghana" })).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: "",
    placeholder: "Select a country…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "zzzznotacountry");
    await waitFor(() => expect(screen.getByText('No results for "zzzznotacountry"')).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: "GH",
    disabled: true,
  },
};
