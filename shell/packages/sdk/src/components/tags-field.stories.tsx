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
