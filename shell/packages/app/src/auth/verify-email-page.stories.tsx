import type { EmailVerificationOutcome } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, fn, userEvent, within } from "storybook/test";
import { VerifyEmailPage } from "./verify-email-page.js";

const withRouter: Decorator = (Story) => {
  const rootRoute = createRootRoute({ component: () => <Story /> });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/verify-email"] }),
  });
  return <RouterProvider router={router} />;
};

const meta = {
  title: "Shell/Auth/VerifyEmailPage",
  component: VerifyEmailPage,
  args: {
    token: "raw-token",
    tenant: "acme",
    verify: async (): Promise<EmailVerificationOutcome> => "signed_in",
    resend: fn(async () => {}),
    redirect: fn(),
  },
  parameters: { layout: "fullscreen" },
  decorators: [withRouter],
} satisfies Meta<typeof VerifyEmailPage>;

export default meta;
type Story = StoryObj<typeof meta>;

async function clickVerify(canvasElement: HTMLElement) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.click(await canvas.findByRole("button", { name: "Verify email" }));
  return canvas;
}

export const Idle: Story = {};

export const Verifying: Story = {
  args: { verify: () => new Promise(() => {}) },
  play: async ({ canvasElement }) => {
    const canvas = await clickVerify(canvasElement);
    await expect(canvas.getByRole("button", { name: "Verify email" })).toHaveAttribute("aria-busy", "true");
  },
};

export const SignedInRedirect: Story = {
  play: async ({ canvasElement, args }) => {
    await clickVerify(canvasElement);
    await expect(args.redirect).toHaveBeenCalledWith("/");
  },
};

export const LoginRequired: Story = {
  args: { verify: async () => "login_required" },
  play: async ({ canvasElement, args }) => {
    await clickVerify(canvasElement);
    await expect(args.redirect).toHaveBeenCalledWith("/auth/login?notice=email_verified");
  },
};

export const ExpiredWithTenant: Story = { args: { token: undefined } };

export const ExpiredWithoutTenant: Story = { args: { token: undefined, tenant: undefined } };

export const ExpiredOnVerify: Story = {
  args: {
    verify: async () => {
      throw new AppError({ code: "invalid_token", message: "link is invalid or has expired", httpStatus: 404 });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await clickVerify(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "This link has expired" })).toHaveFocus();
  },
};

export const ResendSentAndCoolingDown: Story = {
  args: { token: undefined },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Email"), "ada@example.com");
    await userEvent.click(canvas.getByRole("button", { name: "Send a new link" }));
    await expect(await canvas.findByText(/Resend again in 60 seconds/)).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Send a new link" })).toBeDisabled();
    await expect(args.resend).toHaveBeenCalledWith({ email: "ada@example.com", tenant: "acme" });
  },
};

export const Locked: Story = {
  args: {
    verify: async () => {
      throw new AppError({
        code: "rate_limit_exceeded",
        message: "too many requests",
        httpStatus: 429,
        details: { retryAfter: 45 },
      });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await clickVerify(canvasElement);
    await expect(await canvas.findByText(/Too many attempts/)).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Verify email" })).toBeDisabled();
  },
};

export const ErrorOnVerify: Story = {
  args: {
    verify: async () => {
      throw new AppError({ code: "internal_error", message: "email verification failed", httpStatus: 500 });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await clickVerify(canvasElement);
    await expect(await canvas.findByRole("alert")).toHaveTextContent("Couldn't verify your email. Try again.");
  },
};
