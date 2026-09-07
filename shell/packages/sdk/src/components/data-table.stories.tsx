import type { Meta, StoryObj } from "@storybook/react-vite";
import { DataTable, type DataTableColumn } from "./data-table.js";
import { EmptyState } from "./empty-state.js";

interface Contact {
  id: string;
  name: string;
  email: string;
}

const columns: DataTableColumn<Contact>[] = [
  { key: "name", header: "Name", render: (c) => c.name },
  { key: "email", header: "Email", render: (c) => c.email },
];

const contacts: Contact[] = [
  { id: "1", name: "Ama Boateng", email: "ama@example.com" },
  { id: "2", name: "Kwame Mensah", email: "kwame@example.com" },
];

const meta: Meta<typeof DataTable<Contact>> = {
  title: "Data Display/DataTable",
  component: DataTable,
};

export default meta;

type Story = StoryObj<typeof DataTable<Contact>>;

export const Default: Story = {
  args: {
    columns,
    data: contacts,
    keyExtractor: (c) => c.id,
    onRowClick: (c) => console.log(`Navigate to ${c.name}`),
  },
};

export const Loading: Story = {
  args: {
    columns,
    data: [],
    keyExtractor: (c) => c.id,
    isLoading: true,
  },
};

export const Empty: Story = {
  args: {
    columns,
    data: [],
    keyExtractor: (c) => c.id,
    emptyState: <EmptyState title="No contacts" />,
  },
};

export const EmptyWithoutCustomState: Story = {
  args: {
    columns,
    data: [],
    keyExtractor: (c) => c.id,
  },
};

export const NotClickable: Story = {
  args: {
    columns,
    data: contacts,
    keyExtractor: (c) => c.id,
  },
};
