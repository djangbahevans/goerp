import { buildEmptyViewRegistry } from "@goerp/sdk/schema";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { ForbiddenPage } from "./forbidden-page.js";
import { ErrorPageStoryProviders, storyAuth } from "./story-providers.js";

const registry = { ...buildEmptyViewRegistry(), getModuleDisplayName: () => "Sales" };

const meta = {
  title: "Shell/Errors/ForbiddenPage",
  component: ForbiddenPage,
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof ForbiddenPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const MissingPermission: Story = {
  args: { reason: "missing_permission" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
};

export const ModuleNotEnabledAdmin: Story = {
  args: { reason: "module_not_enabled", module: "sales" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders auth={storyAuth(["admin"])} registry={registry}>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
};

export const ModuleNotEnabledMember: Story = {
  args: { reason: "module_not_enabled", module: "sales" },
  decorators: [
    (Story) => (
      <ErrorPageStoryProviders auth={storyAuth(["sales_rep"])} registry={registry}>
        <Story />
      </ErrorPageStoryProviders>
    ),
  ],
};
