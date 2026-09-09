import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, fireEvent, waitFor, within } from "storybook/test";
import { CommandPalette } from "./command-palette.js";
import { commandRegistry } from "./command-registry.js";
import type { Command } from "./command-types.js";

const fakeUser = { id: "u1", email: "demo@goerp.dev", roles: [], amr: [], mfaVerifiedAt: null };
const fakeTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

const fakeAuth: AuthContextValue = {
  state: { status: "authenticated", user: fakeUser, tenant: fakeTenant },
  isAuthenticated: true,
  user: fakeUser,
  tenant: fakeTenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
};

const permissionValue = createPermissionContextValue({
  permissions: new Set(["admin:danger"]),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

const exampleCommands: Command[] = [
  { id: "new-contact", label: "New Contact", group: "Create", action: () => {} },
  { id: "new-order", label: "New Order", group: "Create", action: () => {} },
  { id: "archive-order", label: "Archive Order", group: "Actions", action: () => {} },
  { id: "danger-zone", label: "Danger Zone", group: "Actions", permission: "admin:danger", action: () => {} },
];
commandRegistry.register(exampleCommands);

// CommandPalette has no props — it reads auth/router/query-client/permission
// context directly, so it needs the same providers command-palette.test.tsx wraps it in.
const withProviders: Decorator = (Story) => {
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

const meta: Meta<typeof CommandPalette> = {
  title: "Chrome/CommandPalette",
  component: CommandPalette,
  decorators: [withProviders],
};

export default meta;

type Story = StoryObj<typeof CommandPalette>;

// An empty query shows Recent (visited routes), not these — types a query
// to exercise search and keyboard nav against the example commands instead.
export const Opened: Story = {
  play: async ({ canvasElement }) => {
    fireEvent.keyDown(document, { key: "k", metaKey: true });
    const body = within(canvasElement.ownerDocument.body);
    const dialog = await waitFor(() => body.getByRole("dialog", { name: "Command palette" }));

    const input = within(dialog).getByRole("combobox");
    fireEvent.change(input, { target: { value: "new" } });
    await waitFor(() => within(dialog).getByText("New Contact"));
    within(dialog).getByText("New Order");

    fireEvent.keyDown(input, { key: "ArrowDown" });
    const options = within(dialog).getAllByRole("option");
    await expect(options[1]).toHaveAttribute("aria-selected", "true");
  },
};
