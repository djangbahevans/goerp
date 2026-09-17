import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { TimezoneSelect } from "./timezone-select.js";

const meta: Meta<typeof TimezoneSelect> = {
  title: "Form Fields/TimezoneSelect",
  component: TimezoneSelect,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof TimezoneSelect>;

export const Closed: Story = {
  args: {
    value: "",
    placeholder: "Select a timezone…",
  },
};

// Opens on load so the panel is visible in the sidebar preview, matching
// country-select.stories.tsx's "Open" convention.
export const Open: Story = {
  args: {
    value: "",
    placeholder: "Select a timezone…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getByRole("option", { name: "America/New York" })).toBeInTheDocument());
  },
};

export const ClosedWithValue: Story = {
  args: {
    value: "America/New_York",
  },
};

export const Filtered: Story = {
  args: {
    value: "",
    placeholder: "Select a timezone…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "new york");
    await waitFor(() => expect(screen.getByRole("option", { name: "America/New York" })).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: "",
    placeholder: "Select a timezone…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "zzzznotatimezone");
    await waitFor(() => expect(screen.getByText('No results for "zzzznotatimezone"')).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: "America/New_York",
    disabled: true,
  },
};
