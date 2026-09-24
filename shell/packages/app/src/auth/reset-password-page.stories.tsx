import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { ResetPasswordPage } from "./reset-password-page.js";

const withRouter: Decorator = (Story) => {
  const rootRoute = createRootRoute({ component: () => <Story /> });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/reset-password"] }),
  });
  return <RouterProvider router={router} />;
};

const meta = {
  title: "Shell/Auth/ResetPasswordPage",
  component: ResetPasswordPage,
  args: {
    token: "raw-token",
    tenant: "acme",
    confirmReset: async () => "signed_in" as const,
    redirect: () => {},
  },
  parameters: { layout: "fullscreen" },
  decorators: [withRouter],
} satisfies Meta<typeof ResetPasswordPage>;

export default meta;
type Story = StoryObj<typeof meta>;

async function submitStrongPassword(canvasElement: HTMLElement) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.type(await canvas.findByLabelText("New password"), "Plinth-Quartz-Meadow-47");
  await userEvent.type(canvas.getByLabelText("Confirm new password"), "Plinth-Quartz-Meadow-47");
  await userEvent.click(canvas.getByRole("button", { name: "Set new password" }));
  return canvas;
}

export const Default: Story = {};

export const MissingToken: Story = { args: { token: undefined } };

export const ExpiredOnSubmit: Story = {
  args: {
    confirmReset: async () => {
      throw new AppError({ code: "invalid_token", message: "reset link is invalid or has expired", httpStatus: 404 });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await submitStrongPassword(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "Link expired" })).toHaveFocus();
  },
};

export const RejectedByPolicy: Story = {
  args: {
    confirmReset: async () => {
      throw new AppError({ code: "auth.password_too_weak", message: "password is too common", httpStatus: 422 });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await submitStrongPassword(canvasElement);
    await expect(await canvas.findByText("Password is too common.")).toBeVisible();
  },
};
