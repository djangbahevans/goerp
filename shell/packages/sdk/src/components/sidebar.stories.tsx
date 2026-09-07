import type { Meta, StoryObj } from "@storybook/react-vite";
import { SectionCard } from "./section-card.js";
import { Sidebar } from "./sidebar.js";

const meta: Meta<typeof Sidebar> = {
  title: "Layout/Sidebar",
  component: Sidebar,
};

export default meta;

type Story = StoryObj<typeof Sidebar>;

export const Default: Story = {
  args: {
    children: (
      <SectionCard title="Details">
        <p>Status: Open</p>
        <p>Assignee: Ama Boateng</p>
      </SectionCard>
    ),
  },
};

export const CustomWidth: Story = {
  args: {
    width: 320,
    children: (
      <SectionCard title="Details">
        <p>Status: Open</p>
      </SectionCard>
    ),
  },
};
