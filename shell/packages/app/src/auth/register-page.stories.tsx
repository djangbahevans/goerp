import type { RegisterOutcome, TenantContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, fn, userEvent, within } from "storybook/test";
import { RegisterPage } from "./register-page.js";

const ENABLED: TenantContext = { tenant: null, registrationEnabled: true, termsUrl: null };

// The tenant-context query is pre-seeded so no story hits the network.
function withProviders(tenantContext: TenantContext): Decorator {
  return (Story) => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(["auth", "tenant-context"], tenantContext);
    const rootRoute = createRootRoute({
      component: () => (
        <QueryClientProvider client={queryClient}>
          <Story />
        </QueryClientProvider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute,
      history: createMemoryHistory({ initialEntries: ["/auth/register"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const meta = {
  title: "Shell/Auth/RegisterPage",
  component: RegisterPage,
  args: {
    register: async (): Promise<RegisterOutcome> => ({ kind: "signed_in", tenantSlug: "acme-corp" }),
    checkSlug: async (): Promise<boolean> => true,
    resend: fn(async () => {}),
    redirect: fn(),
  },
  parameters: { layout: "fullscreen" },
  decorators: [withProviders(ENABLED)],
} satisfies Meta<typeof RegisterPage>;

export default meta;
type Story = StoryObj<typeof meta>;

async function fillAndSubmit(canvasElement: HTMLElement) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.type(await canvas.findByLabelText("Full name"), "Kwame Mensah");
  await userEvent.type(canvas.getByLabelText("Email"), "kwame@acme.test");
  await userEvent.type(canvas.getByLabelText("Password"), "Plinth-Quartz-Meadow-47");
  await userEvent.type(canvas.getByLabelText("Confirm password"), "Plinth-Quartz-Meadow-47");
  await userEvent.type(canvas.getByLabelText("Company name"), "Acme Corp");
  const terms = canvas.queryByRole("checkbox");
  if (terms) await userEvent.click(terms);
  await userEvent.click(canvas.getByRole("button", { name: "Create account" }));
  return canvas;
}

export const Default: Story = {};

export const WithTerms: Story = {
  decorators: [withProviders({ ...ENABLED, termsUrl: "https://example.com/terms" })],
};

export const SlugAvailable: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Company name"), "Acme Corp");
    await expect(await canvas.findByText("Available", {}, { timeout: 2000 })).toBeVisible();
  },
};

export const SlugTaken: Story = {
  args: { checkSlug: async () => false },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Company name"), "Acme Corp");
    await expect(await canvas.findByText("Name taken", {}, { timeout: 2000 })).toBeVisible();
  },
};

export const ValidationErrors: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Create account" }));
    await expect(await canvas.findByText("Enter your name.")).toBeVisible();
  },
};

export const Submitting: Story = {
  args: { register: () => new Promise(() => {}) },
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(canvas.getByRole("button", { name: "Create account" })).toHaveAttribute("aria-busy", "true");
  },
};

export const EmailAlreadyInUse: Story = {
  args: {
    register: async () => {
      throw new AppError({ code: "auth.email_already_exists", message: "email already in use", httpStatus: 409 });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(await canvas.findByText("Email already in use")).toBeVisible();
  },
};

export const CheckYourEmail: Story = {
  args: { register: async () => ({ kind: "verification_required", tenantSlug: "acme-corp" }) },
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "Check your email" })).toHaveFocus();
  },
};

export const CheckYourEmailResent: Story = {
  args: { register: async () => ({ kind: "verification_required", tenantSlug: "acme-corp" }) },
  play: async ({ canvasElement, args }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Resend email" }));
    await expect(await canvas.findByText(/Sent\. Check your inbox and spam folder\./)).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Resend email" })).toBeDisabled();
    await expect(args.resend).toHaveBeenCalledWith({ email: "kwame@acme.test", tenant: "acme-corp" });
  },
};

export const ProvisioningPending: Story = {
  args: { register: async () => ({ kind: "provisioning_pending", tenantSlug: "acme-corp" }) },
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "Your workspace is almost ready" })).toHaveFocus();
  },
};
