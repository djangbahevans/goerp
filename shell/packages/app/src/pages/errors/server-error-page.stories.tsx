import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { ServerErrorPage } from "./server-error-page.js";
import { ErrorPageStoryProviders } from "./story-providers.js";

const meta = {
  title: "Shell/Errors/ServerErrorPage",
  component: ServerErrorPage,
  parameters: { layout: "fullscreen" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
  args: { reload: fn() },
} satisfies Meta<typeof ServerErrorPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithTraceId: Story = {
  args: { traceId: "4bf92f3577b34da6a3ce929d0e0e4736" },
};

export const WithoutTraceId: Story = {
  args: { traceId: null },
};
