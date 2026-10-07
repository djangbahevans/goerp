import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { fakeModulesBackend } from "./admin-modules-story-fixtures.js";
import { ModulesPage } from "./modules-page.js";

const meta: Meta<typeof ModulesPage> = {
  title: "Admin/Modules/List",
  component: ModulesPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { onOpenModule: () => {} },
};

export default meta;

type Story = StoryObj<typeof ModulesPage>;

export const Default: Story = {
  name: "installed modules",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getAllByRole("heading", { level: 2 })).toHaveLength(4));
    expect(canvas.getByText("Not on your plan")).toBeInTheDocument();
    expect(canvas.getAllByText("Active")).toHaveLength(3);
  },
};

export const DisabledFilter: Story = {
  name: "disabled filter",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("tab", { name: "Disabled" }));
    await waitFor(() => expect(canvas.getAllByRole("heading", { level: 2 })).toHaveLength(2));
  },
};

export const Empty: Story = {
  name: "no modules",
  beforeEach: fakeModulesBackend({ modules: [] }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("No modules installed")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeModulesBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Couldn't load modules.")).toBeInTheDocument());
  },
};
