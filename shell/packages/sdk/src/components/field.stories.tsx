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

export const RelativeTime: Story = {
  args: {
    label: "Last activity",
    value: new Date(Date.now() - 60 * 60 * 1000),
    type: "relative_time",
  },
};

export const Badge: Story = {
  args: {
    label: "Status",
    value: "confirmed",
    type: "badge",
    badgeConfig: {
      confirmed: { label: "Confirmed", color: "green" },
      draft: { label: "Draft", color: "gray" },
    },
  },
};

export const Avatar: Story = {
  args: {
    label: "Owner",
    value: { name: "Ama Boateng" },
    type: "avatar",
  },
};

export const Country: Story = {
  args: {
    label: "Country",
    value: "GH",
    type: "country",
  },
};

export const Tags: Story = {
  args: {
    label: "Tags",
    value: [
      { id: "1", name: "VIP" },
      { id: "2", name: "Lead" },
    ],
    type: "tags",
  },
};

export const Relation: Story = {
  args: {
    label: "Customer",
    value: { id: "1", display: "Acme Corp" },
    type: "relation",
    href: "/contacts/1",
  },
};

export const File: Story = {
  args: {
    label: "Attachment",
    value: { id: "f1", name: "invoice.pdf" },
    type: "file",
  },
};

export const Color: Story = {
  args: {
    label: "Tag color",
    value: "#357246",
    type: "color",
  },
};

export const Json: Story = {
  args: {
    label: "Metadata",
    value: { source: "import", batchId: "b-1024", rows: 214 },
    type: "json",
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
