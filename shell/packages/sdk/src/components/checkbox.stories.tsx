import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { Checkbox, type CheckboxProps } from "./checkbox.js";
import { TextLink } from "./text-link.js";

function ControlledCheckbox(props: Omit<CheckboxProps, "checked" | "onChange"> & { initialChecked?: boolean }) {
  const { initialChecked = false, ...rest } = props;
  const [checked, setChecked] = useState(initialChecked);
  return <Checkbox {...rest} checked={checked} onChange={setChecked} />;
}

const meta: Meta<typeof ControlledCheckbox> = {
  title: "Form Fields/Checkbox",
  component: ControlledCheckbox,
  args: { label: "Remember this device" },
  decorators: [
    (Story) => (
      <div className="w-80">
        <Story />
      </div>
    ),
  ],
};

export default meta;

type Story = StoryObj<typeof ControlledCheckbox>;

export const Unchecked: Story = {};

export const Checked: Story = { args: { initialChecked: true } };

export const Indeterminate: Story = {
  args: { label: "Select all contacts", indeterminate: true },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("checkbox") as HTMLInputElement;
    await expect(input.indeterminate).toBe(true);
    await expect(input).toHaveAttribute("aria-checked", "mixed");
  },
};

export const Hover: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByText("Remember this device"));
  },
};

export const CheckedHover: Story = {
  args: { initialChecked: true },
  play: async ({ canvasElement }) => {
    await userEvent.hover(within(canvasElement).getByText("Remember this device"));
  },
};

export const FocusVisible: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.tab();
    await expect(within(canvasElement).getByRole("checkbox")).toHaveFocus();
  },
};

export const Invalid: Story = {
  args: { label: "I've saved these codes", error: "Confirm you've saved your recovery codes." },
};

export const Disabled: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      <Checkbox label="Remember this device" checked={false} disabled onChange={() => {}} />
      <Checkbox label="Remember this device" checked disabled onChange={() => {}} />
    </div>
  ),
};

export const WithDescription: Story = {
  args: { label: "Email me a weekly summary", description: "Sent every Monday at 8:00." },
};

export const WithTextLinkInLabel: Story = {
  args: {
    label: (
      <>
        I agree to the{" "}
        <TextLink href="https://example.com/terms" inline external>
          terms of service
        </TextLink>
      </>
    ),
  },
};

export const TwoLineLabel: Story = {
  args: {
    label: "Send me product updates, security notices and occasional surveys about how the platform is working for me",
  },
};

export const LabelHidden: Story = {
  render: function Render() {
    const rows = ["Ada Lovelace", "Grace Hopper", "Katherine Johnson"];
    const [selected, setSelected] = useState<Set<string>>(new Set(["Ada Lovelace"]));
    const toggle = (name: string) =>
      setSelected((prev) => {
        const next = new Set(prev);
        if (next.has(name)) next.delete(name);
        else next.add(name);
        return next;
      });
    return (
      <table className="w-full text-sm text-text">
        <thead>
          <tr className="border-b border-border">
            <th scope="col" className="w-10 p-3 text-left">
              <Checkbox
                label="Select all contacts"
                labelHidden
                checked={selected.size === rows.length}
                indeterminate={selected.size > 0 && selected.size < rows.length}
                onChange={() => setSelected(selected.size === rows.length ? new Set() : new Set(rows))}
              />
            </th>
            <th scope="col" className="p-3 text-left font-medium text-text-secondary">
              Name
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((name) => (
            <tr key={name} className="border-b border-border">
              <td className="p-3">
                <Checkbox label="Select row" labelHidden checked={selected.has(name)} onChange={() => toggle(name)} />
              </td>
              <td className="p-3">{name}</td>
            </tr>
          ))}
        </tbody>
      </table>
    );
  },
};
