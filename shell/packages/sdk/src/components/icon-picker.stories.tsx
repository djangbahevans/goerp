import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { IconPicker } from "./icon-picker.js";

const meta: Meta<typeof IconPicker> = {
  title: "Form Fields/IconPicker",
  component: IconPicker,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof IconPicker>;

export const Closed: Story = {
  args: {
    value: "",
    placeholder: "Choose an icon…",
  },
};

// Opens on load so the panel is visible in the sidebar preview, matching
// country-select.stories.tsx's "Open" convention.
export const Open: Story = {
  args: {
    value: "",
    placeholder: "Choose an icon…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(screen.getAllByRole("gridcell").length).toBeGreaterThan(0));
  },
};

export const ClosedWithValue: Story = {
  args: {
    value: "shopping-cart",
  },
};

export const Filtered: Story = {
  args: {
    value: "",
    placeholder: "Choose an icon…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "shopping-cart");
    await waitFor(() => expect(screen.getByRole("gridcell", { name: "shopping-cart" })).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: "",
    placeholder: "Choose an icon…",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "zzznotarealiconzzz");
    await waitFor(() => expect(screen.getByText('No results for "zzznotarealiconzzz"')).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: "shopping-cart",
    disabled: true,
  },
};
