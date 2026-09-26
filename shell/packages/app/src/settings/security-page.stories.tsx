import type { ActiveSession } from "@goerp/sdk/auth";
import { Toast } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { SecurityPage, type SessionsClient } from "./security-page.js";

function ago(minutes: number): string {
  return new Date(Date.now() - minutes * 60_000).toISOString();
}

const THIS_DEVICE: ActiveSession = {
  id: "fam-this",
  userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36",
  ipAddress: "41.66.18.2",
  countryCode: "GH",
  signedInAt: ago(60 * 24 * 3),
  lastActiveAt: ago(0),
  persistent: true,
  current: true,
};

const OTHER_SESSIONS: ActiveSession[] = [
  {
    id: "fam-phone",
    userAgent:
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
    ipAddress: "102.176.4.9",
    countryCode: "GH",
    signedInAt: ago(60 * 24 * 12),
    lastActiveAt: ago(45),
    persistent: true,
    current: false,
  },
  {
    id: "fam-office",
    userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/130.0",
    ipAddress: "185.23.91.14",
    countryCode: "GB",
    signedInAt: ago(60 * 24 * 20),
    lastActiveAt: ago(60 * 26),
    persistent: false,
    current: false,
  },
  {
    id: "fam-unknown",
    userAgent: null,
    ipAddress: null,
    countryCode: null,
    signedInAt: ago(60 * 24 * 30),
    lastActiveAt: ago(60 * 24 * 6),
    persistent: true,
    current: false,
  },
];

// An in-memory client, so Sign out really removes the session and a
// refetch reflects it.
function memoryClient(initial: ActiveSession[]): SessionsClient {
  let sessions = [...initial];
  return {
    list: async () => sessions,
    revoke: async (id) => {
      sessions = sessions.filter((s) => s.id !== id);
    },
    revokeOthers: async () => {
      const revoked = sessions.filter((s) => !s.current).length;
      sessions = sessions.filter((s) => s.current);
      return revoked;
    },
  };
}

// A router for the Password link and a fresh query client per story, so
// one story's cached sessions can't leak into the next.
const withShell: Decorator = (Story) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <Story />
        <Toast />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
  });
  return <RouterProvider router={router} />;
};

const meta: Meta<typeof SecurityPage> = {
  title: "Shell/Settings/SecurityPage",
  component: SecurityPage,
  decorators: [withShell],
};

export default meta;
type Story = StoryObj<typeof meta>;

export const SeveralSessions: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])} />,
};

export const OnlyThisDevice: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("You're not signed in anywhere else.")).toBeInTheDocument();
    await expect(canvas.queryByRole("button", { name: "Sign out all other sessions" })).toBeNull();
  },
};

export const Loading: Story = {
  render: () => (
    <SecurityPage
      client={{
        ...memoryClient([]),
        list: () => new Promise<ActiveSession[]>(() => {}),
      }}
    />
  ),
};

export const LoadError: Story = {
  render: () => (
    <SecurityPage
      client={{
        ...memoryClient([]),
        list: async () => {
          throw new AppError({ code: "internal_error", message: "list sessions failed", httpStatus: 500 });
        },
      }}
    />
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};

export const SignOutOneSession: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    const [firstSignOut] = await canvas.findAllByRole("button", { name: "Sign out" });
    await userEvent.click(firstSignOut as HTMLElement);
    await userEvent.click(within(await body.findByRole("alertdialog")).getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(canvas.queryByText("Safari on iOS")).toBeNull());
  },
};

export const SignOutAllOthers: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Sign out all other sessions" }));
    await userEvent.click(within(await body.findByRole("alertdialog")).getByRole("button", { name: "Sign out all" }));
    await expect(await body.findByText("Signed out of 3 other sessions")).toBeInTheDocument();
    await expect(await canvas.findByText("You're not signed in anywhere else.")).toBeInTheDocument();
  },
};
