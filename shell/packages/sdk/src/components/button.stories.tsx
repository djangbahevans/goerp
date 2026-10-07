import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { Button } from "./button.js";
import { Checkbox } from "./checkbox.js";
import { IconButton } from "./icon-button.js";
import { ToggleField } from "./toggle-field.js";

const meta: Meta<typeof Button> = {
  title: "Actions/Button",
  component: Button,
  args: {
    children: "Save changes",
  },
};

export default meta;

type Story = StoryObj<typeof Button>;

export const Primary: Story = { args: { variant: "primary", icon: "check" } };

export const Secondary: Story = { args: { variant: "secondary" } };

export const Ghost: Story = { args: { variant: "ghost" } };

export const Danger: Story = { args: { variant: "danger", children: "Archive", icon: "archive" } };

export const Link: Story = {
  render: () => (
    <p className="text-sm text-text">
      Can't reach your authenticator? <Button variant="link">Use a recovery code instead</Button>
    </p>
  ),
};

export const Sizes: Story = {
  render: () => (
    <div className="flex items-center gap-2">
      <Button variant="primary" size="sm" icon="plus">
        Small
      </Button>
      <Button variant="primary" icon="plus">
        Medium
      </Button>
      <IconButton icon="more-vertical" label="More" size="sm" variant="secondary" />
      <IconButton icon="more-vertical" label="More" variant="secondary" />
    </div>
  ),
};

export const AllVariants: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      {(["md", "sm"] as const).map((size) => (
        <div key={size} className="flex items-center gap-2">
          <Button variant="primary" size={size}>
            Primary
          </Button>
          <Button variant="secondary" size={size}>
            Secondary
          </Button>
          <Button variant="ghost" size={size}>
            Ghost
          </Button>
          <Button variant="danger" size={size}>
            Danger
          </Button>
          <span className="text-sm">
            <Button variant="link" size={size}>
              Link
            </Button>
          </span>
        </div>
      ))}
    </div>
  ),
};

export const Hover: Story = {
  args: { variant: "secondary" },
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByRole("button"));
  },
};

export const FocusVisible: Story = {
  args: { variant: "primary" },
  play: async ({ canvasElement }) => {
    await userEvent.tab();
    await expect(within(canvasElement).getByRole("button")).toHaveFocus();
  },
};

export const Loading: Story = { args: { variant: "primary", loading: true, icon: "check" } };

export const Disabled: Story = {
  render: () => (
    <div className="flex items-center gap-2">
      {(["primary", "secondary", "ghost", "danger", "link"] as const).map((variant) => (
        <Button key={variant} variant={variant} disabled>
          {variant}
        </Button>
      ))}
    </div>
  ),
};

// A submit that turns loading keeps focus and ignores a second click.
export const SubmitKeepsFocusWhileLoading: Story = {
  render: function Render() {
    const [submits, setSubmits] = useState(0);
    return (
      <form
        className="flex w-72 flex-col gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          setSubmits((n) => n + 1);
        }}
      >
        <Button type="submit" variant="primary" fullWidth loading={submits > 0}>
          Sign in
        </Button>
        <output className="text-sm text-text-secondary">Submitted {submits} time(s)</output>
      </form>
    );
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const button = canvas.getByRole("button", { name: "Sign in" });
    await userEvent.click(button);
    await userEvent.click(button);
    await expect(button).toHaveFocus();
    await expect(button).toHaveAttribute("aria-busy", "true");
    await expect(canvas.getByText("Submitted 1 time(s)")).toBeInTheDocument();
  },
};

export const FullWidth: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-2">
      <Button variant="primary" fullWidth>
        Sign in
      </Button>
      <Button fullWidth>Resend email</Button>
    </div>
  ),
};

export const AsLink: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-2">
      <Button href="#sign-in" variant="primary" fullWidth>
        Back to sign in
      </Button>
      <Button href="#sign-in" variant="primary" fullWidth disabled>
        Disabled link
      </Button>
    </div>
  ),
};

export const IconButtonVariants: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      {(["md", "sm"] as const).map((size) => (
        <div key={size} className="flex items-center gap-2">
          <IconButton icon="x" label="Dismiss" size={size} />
          <IconButton icon="download" label="Download" variant="secondary" size={size} />
          <IconButton icon="trash-2" label="Delete" variant="danger" size={size} />
          <IconButton icon="x" label="Dismiss (disabled)" size={size} disabled />
        </div>
      ))}
    </div>
  ),
};

export const IconButtonHover: Story = {
  render: () => <IconButton icon="x" label="Dismiss" />,
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByRole("button"));
  },
};

export const IconButtonFocusVisible: Story = {
  render: () => <IconButton icon="x" label="Dismiss" />,
  play: async ({ canvasElement }) => {
    await userEvent.tab();
    await expect(within(canvasElement).getByRole("button")).toHaveFocus();
  },
};

export const IconButtonPressed: Story = {
  render: function Render() {
    const [bold, setBold] = useState(true);
    const [italic, setItalic] = useState(false);
    return (
      <div role="toolbar" aria-label="Formatting" className="flex items-center gap-1">
        <IconButton icon="bold" label="Bold" size="sm" pressed={bold} onClick={() => setBold((v) => !v)} />
        <IconButton icon="italic" label="Italic" size="sm" pressed={italic} onClick={() => setItalic((v) => !v)} />
      </div>
    );
  },
};

const PHONE_VIEWPORT = {
  viewport: { options: { phone360: { name: "Phone (360px)", styles: { width: "360px", height: "740px" } } } },
};

// shell-architecture.md §23: below 768px every control has a 44×44px hit area, at every size and variant.
export const TouchTargetsOnPhone: Story = {
  name: "below 768px: every size and variant is at least 44×44px",
  parameters: PHONE_VIEWPORT,
  globals: { viewport: { value: "phone360", isRotated: false } },
  render: () => {
    const [on, setOn] = useState(false);
    const [checked, setChecked] = useState(false);
    return (
      <div className="flex flex-wrap items-center gap-6 p-4">
        <Button variant="primary" size="sm">
          OK
        </Button>
        <Button variant="secondary">Save</Button>
        <Button variant="ghost" size="sm" icon="plus">
          Add
        </Button>
        <Button variant="link">Skip</Button>
        <IconButton icon="x" label="Close small" size="sm" />
        <IconButton icon="x" label="Close" />
        <Checkbox label="Select row" labelHidden checked={checked} onChange={setChecked} />
        <ToggleField value={on} onChange={setOn} />
      </div>
    );
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    for (const control of [
      canvas.getByRole("button", { name: "OK" }),
      canvas.getByRole("button", { name: "Save" }),
      canvas.getByRole("button", { name: "Add" }),
      canvas.getByRole("button", { name: "Close small" }),
      canvas.getByRole("button", { name: "Close" }),
    ]) {
      const { width, height } = control.getBoundingClientRect();
      await expect(Math.round(width)).toBeGreaterThanOrEqual(44);
      await expect(Math.round(height)).toBeGreaterThanOrEqual(44);
    }
    // A link button, a checkbox and a switch enlarge their hit area without growing their layout box, so it is hit-tested.
    for (const control of [
      canvas.getByRole("button", { name: "Skip" }),
      canvas.getByRole("checkbox", { name: "Select row" }),
      canvas.getByRole("switch"),
    ]) {
      const label = (control.closest("label") ?? control) as HTMLElement;
      const { x, y, width, height } = label.getBoundingClientRect();
      for (const [px, py] of [
        [x + width / 2 - 20, y + height / 2 - 20],
        [x + width / 2 + 20, y + height / 2 + 20],
      ] as const) {
        await expect(label.contains(canvasElement.ownerDocument.elementFromPoint(px, py))).toBe(true);
      }
    }
  },
};
