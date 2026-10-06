import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { fakeConnectorsBackend } from "./admin-connectors-story-fixtures.js";
import { ConnectorsPage } from "./connectors-page.js";

const meta: Meta<typeof ConnectorsPage> = {
  title: "Admin/Connectors/List",
  component: ConnectorsPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { onOpenConnector: () => {} },
};

export default meta;

type Story = StoryObj<typeof ConnectorsPage>;

export const Default: Story = {
  name: "installed connectors",
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getAllByRole("heading", { level: 2 })).toHaveLength(3));
    expect(canvas.getByText("Primary")).toBeInTheDocument();
    expect(canvas.getByText("Not configured")).toBeInTheDocument();
  },
};

export const Empty: Story = {
  name: "no connectors",
  beforeEach: fakeConnectorsBackend({ connectors: [] }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("No connectors installed")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeConnectorsBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Couldn't load connectors.")).toBeInTheDocument());
  },
};
