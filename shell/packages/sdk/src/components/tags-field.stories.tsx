import type { Meta, StoryObj } from "@storybook/react-vite";
import { TagsField } from "./tags-field.js";

const options = [
  { id: "1", name: "VIP" },
  { id: "2", name: "Wholesale" },
  { id: "3", name: "Newsletter" },
];

const meta: Meta<typeof TagsField> = {
  title: "Form Fields/TagsField",
  component: TagsField,
  args: {
    onChange: () => {},
    options,
  },
};

export default meta;

type Story = StoryObj<typeof TagsField>;

export const Default: Story = {
  args: {
    label: "Tags",
    value: [{ id: "1", name: "VIP" }],
  },
};

export const Creatable: Story = {
  args: {
    label: "Tags",
    value: [],
    creatable: true,
    onCreate: (name: string) => ({ id: crypto.randomUUID(), name }),
  },
};

export const WithError: Story = {
  args: {
    label: "Tags",
    value: [],
    error: "At least one tag is required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Tags",
    value: [{ id: "1", name: "VIP" }],
    disabled: true,
  },
};

// Exercises pillTextClassFor's WCAG luminance computation: a light hex picks
// black text, a dark hex picks white text.
export const ColoredTags: Story = {
  args: {
    label: "Tags",
    value: [
      { id: "1", name: "VIP", color: "#FFFF00" },
      { id: "2", name: "Wholesale", color: "#000080" },
    ],
  },
};
