import type { Meta, StoryObj } from "@storybook/react-vite";
import { SectionCard } from "./section-card.js";

const meta: Meta<typeof SectionCard> = {
  title: "Layout/SectionCard",
  component: SectionCard,
};

export default meta;

type Story = StoryObj<typeof SectionCard>;

export const Default: Story = {
  args: {
    title: "Contact Information",
    children: <p>Fields go here</p>,
  },
};

export const Collapsible: Story = {
  args: {
    title: "Employment Details",
    collapsible: true,
    children: <p>Fields go here</p>,
  },
};

export const CollapsedByDefault: Story = {
  args: {
    title: "Employment Details",
    collapsible: true,
    defaultCollapsed: true,
    children: <p>Fields go here</p>,
  },
};

export const Untitled: Story = {
  args: {
    children: <p>Fields go here</p>,
  },
};
