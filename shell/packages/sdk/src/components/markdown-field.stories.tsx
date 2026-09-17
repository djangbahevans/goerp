import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { MarkdownField } from "./markdown-field.js";

const meta: Meta<typeof MarkdownField> = {
  title: "Form Fields/MarkdownField",
  component: MarkdownField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof MarkdownField>;

export const Default: Story = {
  args: {
    label: "Notes",
    value: "Follow up next week about the renewal.",
  },
};

export const WithPlaceholder: Story = {
  args: {
    label: "Notes",
    value: "",
    placeholder: "Write something…",
  },
};

export const Formatted: Story = {
  args: {
    label: "Notes",
    value:
      "**Urgent** — see the [renewal policy](https://example.com/renewal-policy) before responding:\n\n- Confirm the new term length\n- Check for a price change",
  },
};

export const WithHeading: Story = {
  args: {
    label: "Email template",
    value: "## Renewal reminder\n\nYour plan renews on the 1st — no action needed.",
  },
};

// Covers this component's two toolbar deltas from RichTextField: an
// ordered list and a fenced code block, both markdown-specific additions
// documented in markdown-field.md.
export const WithOrderedListAndCode: Story = {
  args: {
    label: "Runbook",
    value: "1. Rotate the API key\n2. Redeploy the service\n\n```\ncurl -X POST /api/keys/rotate\n```",
  },
};

// Toggles a toolbar command first (rather than requiring a manual keystroke
// in Storybook's UI) so Undo renders enabled, then undoes it so Redo does
// too — a button click is a more reliable scripted edit here than typing
// into the contenteditable region, matching this file's own test suite.
export const WithUndoRedoAvailable: Story = {
  args: {
    label: "Notes",
    value: "Follow up next week about the renewal.",
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
    value: "See the [renewal policy](https://example.com/renewal-policy).",
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
    value: "Follow up next week about the renewal.",
    disabled: true,
  },
};
