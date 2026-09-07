import type { Meta, StoryObj } from "@storybook/react-vite";
import { ActionButton } from "./action-button.js";
import { PageHeader } from "./page-header.js";

const meta: Meta<typeof PageHeader> = {
  title: "Layout/PageHeader",
  component: PageHeader,
};

export default meta;

type Story = StoryObj<typeof PageHeader>;

export const Default: Story = {
  args: {
    title: "Contacts",
    subtitle: "4,823 contacts",
    actions: (
      <ActionButton icon="plus" onClick={() => {}}>
        New Contact
      </ActionButton>
    ),
  },
};

export const TitleOnly: Story = {
  args: {
    title: "Contacts",
  },
};
