import type { Meta, StoryObj } from "@storybook/react-vite";
import { PageHeader } from "./page-header.js";
import { PageLayout } from "./page-layout.js";

const meta: Meta<typeof PageLayout> = {
  title: "Layout/PageLayout",
  component: PageLayout,
};

export default meta;

type Story = StoryObj<typeof PageLayout>;

export const Default: Story = {
  args: {
    children: (
      <>
        <PageHeader title="Contacts" subtitle="4,823 contacts" />
        <p>Page content goes here.</p>
      </>
    ),
  },
};
