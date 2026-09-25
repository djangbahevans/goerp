import type { Meta, StoryObj } from "@storybook/react-vite";
import { ErrorPageStoryProviders } from "./story-providers.js";
import { TenantSuspendedPage } from "./tenant-suspended-page.js";

const meta = {
  title: "Shell/Errors/TenantSuspendedPage",
  component: TenantSuspendedPage,
  parameters: { layout: "fullscreen" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
} satisfies Meta<typeof TenantSuspendedPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithSupportUrl: Story = {
  args: { supportUrl: "mailto:support@example.com" },
};

export const WithoutSupportUrl: Story = {};
