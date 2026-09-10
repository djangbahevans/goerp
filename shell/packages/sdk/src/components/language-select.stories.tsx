import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { LanguageSelect } from "./language-select.js";

const meta: Meta<typeof LanguageSelect> = {
  title: "Form Fields/LanguageSelect",
  component: LanguageSelect,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof LanguageSelect>;

export const Closed: Story = {
  args: {
    value: "",
    placeholder: "Select a language…",
  },
};

export const Open: Story = {
  args: {
    value: "",
    placeholder: "Select a language…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "French" })).toBeInTheDocument());
  },
};

export const ClosedWithValue: Story = {
  args: {
    value: "fr",
  },
};

export const Filtered: Story = {
  args: {
    value: "",
    placeholder: "Select a language…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "French");
    await waitFor(() => expect(screen.getByRole("option", { name: "French" })).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: "",
    placeholder: "Select a language…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "notalanguage");
    await waitFor(() => expect(screen.getByText('No results for "notalanguage"')).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: "fr",
    disabled: true,
  },
};
