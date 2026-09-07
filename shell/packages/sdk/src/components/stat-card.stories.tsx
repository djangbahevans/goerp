import type { Meta, StoryObj } from "@storybook/react-vite";
import { StatCard } from "./stat-card.js";

const meta: Meta<typeof StatCard> = {
  title: "Data Display/StatCard",
  component: StatCard,
};

export default meta;

type Story = StoryObj<typeof StatCard>;

export const Default: Story = {
  args: {
    label: "Total Contacts",
    value: 4823,
    change: { value: 12, direction: "up", period: "this month" },
    icon: "users",
    href: "/contacts",
  },
};

export const Currency: Story = {
  args: {
    label: "Revenue",
    value: 482300,
    format: "currency",
    currency: "GHS",
  },
};

export const AtRisk: Story = {
  args: {
    label: "Low Stock Items",
    value: 7,
    color: "orange",
    href: "/inventory/products?filter[stock_status]=low",
  },
};

export const Loading: Story = {
  args: {
    label: "Total Contacts",
    value: undefined,
  },
};
