import type { Meta, StoryObj } from "@storybook/react-vite";
import { RelationField } from "./relation-field.js";

const meta: Meta<typeof RelationField> = {
  title: "Form Fields/RelationField",
  component: RelationField,
};

export default meta;

type Story = StoryObj<typeof RelationField>;

export const Default: Story = {
  args: {
    label: "Customer",
    value: { id: "1", display: "Acme Inc" },
    href: "/contacts/1",
  },
};

export const Unlinked: Story = {
  args: {
    label: "Customer",
    value: { id: "1", display: "Acme Inc" },
  },
};

export const Empty: Story = {
  args: {
    label: "Customer",
  },
};
