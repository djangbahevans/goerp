import type { TenantContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { ForgotPasswordPage } from "./forgot-password-page.js";

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
      history: createMemoryHistory({ initialEntries: ["/auth/forgot-password"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const SUBDOMAIN: TenantContext = {
  tenant: { slug: "acme", name: "Acme Corp" },
  registrationEnabled: false,
  termsUrl: null,
};
const SHARED_DOMAIN: TenantContext = { tenant: null, registrationEnabled: false, termsUrl: null };

const meta = {
  title: "Shell/Auth/ForgotPasswordPage",
  component: ForgotPasswordPage,
  args: { requestReset: async () => {} },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof ForgotPasswordPage>;

export default meta;
type Story = StoryObj<typeof meta>;

async function submitEmail(canvasElement: HTMLElement) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.type(await canvas.findByLabelText("Email"), "ada@example.com");
  await userEvent.click(canvas.getByRole("button", { name: "Send reset link" }));
  return canvas;
}

export const SubdomainTenant: Story = { decorators: [withProviders(SUBDOMAIN)] };

export const SharedDomain: Story = { decorators: [withProviders(SHARED_DOMAIN)] };

export const Sent: Story = {
  decorators: [withProviders(SUBDOMAIN)],
  play: async ({ canvasElement }) => {
    const canvas = await submitEmail(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "Check your email" })).toHaveFocus();
  },
};

export const RateLimited: Story = {
  decorators: [withProviders(SUBDOMAIN)],
  args: {
    requestReset: async () => {
      throw new AppError({
        code: "rate_limit_exceeded",
        message: "too many requests",
        httpStatus: 429,
        details: { retryAfter: 60 },
      });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = await submitEmail(canvasElement);
    await expect(await canvas.findByText(/Too many requests/)).toBeVisible();
  },
};
