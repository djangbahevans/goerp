import type { InviteAcceptOutcome, InviteInfo } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { AcceptInvitePage } from "./accept-invite-page.js";

const withProviders: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <Story />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/accept-invite"] }),
  });
  return <RouterProvider router={router} />;
};

const NEW_USER: InviteInfo = { tenantName: "Acme Corp", email: "kwame@acme.com", name: null, passwordRequired: true };

const meta = {
  title: "Shell/Auth/AcceptInvitePage",
  component: AcceptInvitePage,
  args: {
    token: "raw-token",
    tenant: "acme",
    loadInfo: async () => NEW_USER,
    accept: async (): Promise<InviteAcceptOutcome> => "signed_in",
    redirect: () => {},
  },
  parameters: { layout: "fullscreen" },
  decorators: [withProviders],
} satisfies Meta<typeof AcceptInvitePage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const NewAccount: Story = {};

export const ExistingAccount: Story = {
  args: {
    loadInfo: async () => ({ ...NEW_USER, passwordRequired: false }),
    accept: async (): Promise<InviteAcceptOutcome> => "login_required",
  },
};

export const Expired: Story = {
  args: {
    loadInfo: async () => {
      throw new AppError({ code: "invalid_invite", message: "invite link is invalid or has expired", httpStatus: 404 });
    },
  },
};
