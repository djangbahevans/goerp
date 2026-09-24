import type { AuthContextValue, TenantContext } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, fn, userEvent, within } from "storybook/test";
import { LoginPage } from "./login-page.js";

function authWith(login: AuthContextValue["login"]): AuthContextValue {
  return {
    state: { status: "unauthenticated" },
    isAuthenticated: false,
    user: null,
    tenant: null,
    login,
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
}

// The tenant-context query is pre-seeded so no story hits the network.
function withProviders(tenantContext: TenantContext, auth: AuthContextValue): Decorator {
  return (Story) => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(["auth", "tenant-context"], tenantContext);
    const rootRoute = createRootRoute({
      component: () => (
        <QueryClientProvider client={queryClient}>
          <AuthContext.Provider value={auth}>
            <Story />
          </AuthContext.Provider>
        </QueryClientProvider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute,
      history: createMemoryHistory({ initialEntries: ["/auth/login"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const SUBDOMAIN: TenantContext = {
  tenant: { slug: "acme", name: "Acme Corp" },
  registrationEnabled: false,
  termsUrl: null,
};
const SHARED_DOMAIN: TenantContext = { tenant: null, registrationEnabled: true, termsUrl: null };

const meta = {
  title: "Shell/Auth/LoginPage",
  component: LoginPage,
  args: { redirectTo: "/" },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof LoginPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SubdomainTenant: Story = {
  decorators: [
    withProviders(
      SUBDOMAIN,
      authWith(async () => {}),
    ),
  ],
};

export const SharedDomainWithRegistration: Story = {
  decorators: [
    withProviders(
      SHARED_DOMAIN,
      authWith(async () => {}),
    ),
  ],
};

async function fillAndSubmit(canvasElement: HTMLElement) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.type(await canvas.findByLabelText("Email"), "ada@example.com");
  await userEvent.type(canvas.getByLabelText("Password"), "wrong-password");
  await userEvent.click(canvas.getByRole("button", { name: "Sign in" }));
  return canvas;
}

export const InvalidCredentials: Story = {
  decorators: [
    withProviders(
      SUBDOMAIN,
      authWith(async () => {
        throw new AppError({ code: "invalid_credentials", message: "invalid email or password", httpStatus: 401 });
      }),
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(await canvas.findByText("Invalid email or password")).toBeVisible();
  },
};

export const LockedOut: Story = {
  decorators: [
    withProviders(
      SUBDOMAIN,
      authWith(async () => {
        throw new AppError({
          code: "rate_limit_exceeded",
          message: "too many requests",
          httpStatus: 429,
          details: { retryAfter: 60 },
        });
      }),
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await expect(await canvas.findByText(/Too many attempts/)).toBeVisible();
  },
};

export const EmailVerifiedNotice: Story = {
  args: { notice: "email_verified" },
  decorators: [
    withProviders(
      SUBDOMAIN,
      authWith(async () => {}),
    ),
  ],
};

export const EmailVerificationRequired: Story = {
  args: { resendVerification: fn(async () => {}) },
  decorators: [
    withProviders(
      SUBDOMAIN,
      authWith(async () => {
        throw new AppError({ code: "email_verification_required", message: "", httpStatus: 403 });
      }),
    ),
  ],
  play: async ({ canvasElement, args }) => {
    const canvas = await fillAndSubmit(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Resend verification email" }));
    await expect(await canvas.findByText(/a new link is on its way/)).toBeVisible();
    await expect(args.resendVerification).toHaveBeenCalledWith({ email: "ada@example.com", tenant: "acme" });
  },
};
