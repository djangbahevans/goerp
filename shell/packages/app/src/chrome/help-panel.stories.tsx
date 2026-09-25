import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, waitFor, within } from "storybook/test";
import { commandRegistry } from "./command-registry.js";
import { type HelpLink, HelpPanel } from "./help-panel.js";

const user = {
  id: "u1",
  email: "demo@goerp.dev",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: ["admin"],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
};
const tenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

const fakeAuth: AuthContextValue = {
  state: { status: "authenticated", user, tenant },
  isAuthenticated: true,
  user,
  tenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

const allLinks: HelpLink[] = [
  { label: "Documentation", icon: "book-open", href: "https://docs.example.com" },
  { label: "API reference", icon: "code", href: "https://api.example.com" },
  { label: "Status page", icon: "activity", href: "https://status.example.com" },
  { label: "What's new", icon: "sparkles", href: "https://example.com/changelog" },
];

// The shortcuts section reads auth, router, query client, permissions and
// the global command registry, so each story gets those here.
const withShell: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={fakeAuth}>
          <PermissionContext.Provider value={permissionValue}>
            <Story />
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return <RouterProvider router={router} />;
};

const meta: Meta<typeof HelpPanel> = {
  title: "Chrome/HelpPanel",
  component: HelpPanel,
  decorators: [withShell],
  args: { open: true, onClose: () => {}, links: allLinks },
  beforeEach: () =>
    commandRegistry.register([{ id: "new-contact", label: "New Contact", shortcut: "N C", action: () => {} }]),
};

export default meta;

type Story = StoryObj<typeof HelpPanel>;

async function panel(canvasElement: HTMLElement) {
  const body = within(canvasElement.ownerDocument.body);
  return waitFor(() => body.getByRole("dialog", { name: "Help" }));
}

export const AllLinks: Story = {
  play: async ({ canvasElement }) => {
    const dialog = await panel(canvasElement);
    await expect(within(dialog).getAllByRole("link")).toHaveLength(4);
    await expect(within(dialog).getByRole("heading", { name: "Commands" })).toBeInTheDocument();
  },
};

export const SomeLinks: Story = {
  args: { links: allLinks.map((link) => (link.label === "Documentation" ? link : { ...link, href: undefined })) },
  play: async ({ canvasElement }) => {
    const dialog = await panel(canvasElement);
    await expect(within(dialog).getAllByRole("link")).toHaveLength(1);
  },
};

export const NoLinks: Story = {
  args: { links: [] },
  play: async ({ canvasElement }) => {
    const dialog = await panel(canvasElement);
    await expect(within(dialog).queryByRole("region", { name: "Links" })).toBeNull();
    await expect(within(dialog).getByRole("region", { name: "Keyboard shortcuts" })).toBeInTheDocument();
  },
};
