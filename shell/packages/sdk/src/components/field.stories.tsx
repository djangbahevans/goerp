import type { Meta, StoryObj } from "@storybook/react-vite";
import { Field } from "./field.js";

const meta: Meta<typeof Field> = {
  title: "Form Fields/Field",
  component: Field,
};

export default meta;

type Story = StoryObj<typeof Field>;

export const Text: Story = {
  args: {
    label: "Name",
    value: "Ama Boateng",
  },
};

export const Email: Story = {
  args: {
    label: "Email",
    value: "ama@example.com",
    type: "email",
  },
};

export const Currency: Story = {
  args: {
    label: "Amount",
    value: 10000,
    type: "currency",
    currency: "GHS",
  },
};

export const Datetime: Story = {
  args: {
    label: "Created",
    value: "2026-03-05T10:30:00.000Z",
    type: "datetime",
  },
};

export const Linked: Story = {
  args: {
    label: "Manager",
    value: "Kwame Mensah",
    href: "/contacts/1",
  },
};

export const Empty: Story = {
  args: {
    label: "Email",
    value: null,
  },
};
