import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { WorkspaceNotFound } from "./workspace-not-found.js";

const meta = {
  title: "Shell/Auth/WorkspaceNotFound",
  component: WorkspaceNotFound,
  args: { appUrl: "https://app.goerp.io", hostname: "acmcorp.goerp.io" },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof WorkspaceNotFound>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const heading = canvas.getByRole("heading", { name: "Workspace not found" });
    await expect(heading).toHaveFocus();
    await expect(canvas.getByRole("link", { name: "Go to sign in" })).toHaveAttribute(
      "href",
      "https://app.goerp.io/auth/login",
    );
  },
};

export const LongHostname: Story = {
  args: { hostname: "a-very-long-mistyped-workspace-name-that-keeps-going.eu-west.goerp.io" },
};
