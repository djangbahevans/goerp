import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { RichTextField } from "./rich-text-field.js";

const meta: Meta<typeof RichTextField> = {
  title: "Form Fields/RichTextField",
  component: RichTextField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof RichTextField>;

export const Default: Story = {
  args: {
    label: "Notes",
    value: "<p>Follow up next week about the renewal.</p>",
  },
};

export const Formatted: Story = {
  args: {
    label: "Notes",
    value:
      '<p><strong>Urgent</strong> — see the <a href="https://example.com/renewal-policy">renewal policy</a> before responding:</p><ul><li><p>Confirm the new term length</p></li><li><p>Check for a price change</p></li></ul>',
  },
};

export const WithHeading: Story = {
  args: {
    label: "Email template",
    value: "<h2>Renewal reminder</h2><p>Your plan renews on the 1st — no action needed.</p>",
  },
};

// Toggles a toolbar command first (rather than requiring a manual keystroke
// in Storybook's UI) so Undo renders enabled, then undoes it so Redo does
// too — a button click is a more reliable scripted edit here than typing
// into the contenteditable region, matching this file's own test suite.
export const WithUndoRedoAvailable: Story = {
  args: {
    label: "Notes",
    value: "<p>Follow up next week about the renewal.</p>",
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Bulleted list" }));
    await userEvent.click(canvas.getByRole("button", { name: "Undo" }));
  },
};

// Opens on load (rather than requiring a manual click in Storybook's UI) so
// the link popover — pre-filled with the existing href, plus its "open in a
// new tab" and "remove link" actions — is visible in the sidebar preview.
export const WithLinkPopoverOpen: Story = {
  args: {
    label: "Notes",
    value: '<p>See the <a href="https://example.com/renewal-policy">renewal policy</a>.</p>',
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // Places the cursor inside the link's own text first — Storybook's play
    // functions run in a real browser, where a click genuinely positions
    // the selection there, unlike a jsdom unit test.
    await userEvent.click(canvas.getByText("renewal policy"));
    await userEvent.click(canvas.getByRole("button", { name: "Link" }));
  },
};

export const WithError: Story = {
  args: {
    label: "Notes",
    value: "",
    error: "Notes are required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Notes",
    value: "<p>Follow up next week about the renewal.</p>",
    disabled: true,
  },
};
