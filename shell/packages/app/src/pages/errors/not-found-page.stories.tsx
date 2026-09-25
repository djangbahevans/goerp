import type { Meta, StoryObj } from "@storybook/react-vite";
import { NotFoundPage } from "./not-found-page.js";
import { ErrorPageStoryProviders } from "./story-providers.js";

const meta = {
  title: "Shell/Errors/NotFoundPage",
  component: NotFoundPage,
  parameters: { layout: "fullscreen" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
} satisfies Meta<typeof NotFoundPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
