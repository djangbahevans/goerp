import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { ModuleNavigationProvider } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, waitFor, within } from "storybook/test";
import { commandRegistry } from "../chrome/command-registry.js";
import type { Command } from "../chrome/command-types.js";
import { createQueryClient } from "../query-client.js";
import { useModuleNavigate } from "../router/use-module-navigate.js";
import { openKeyboardShortcuts } from "./keyboard-shortcuts-control.js";
import { KeyboardShortcutsDialog } from "./keyboard-shortcuts-dialog.js";

function fakeAuth(roles: string[]): AuthContextValue {
  const user = {
    id: "u1",
    email: "demo@goerp.dev",
    name: null,
    contactId: null,
    avatarUrl: null,
    roles,
    amr: [],
    mfaVerifiedAt: null,
    mfaSetupRequired: false,
    passwordChangeRequired: false,
    passwordMinLength: 12,
    phone: null,
    title: null,
    theme: "system" as const,
    contrast: "system" as const,
    locale: null,
    timezone: null,
    dateFormat: null,
  };
  const tenant = {
    id: "t1",
    slug: "acme",
    name: "Acme",
    plan: "pro",
    defaultLocale: "en",
    defaultTimezone: "UTC",
    defaultCurrency: "",
    availableLocales: ["en"],
    firstDayOfWeek: "monday" as const,
    numberFormat: "1,234.56" as const,
    passwordMinLength: 12,
  };
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: async () => null,
    completeHandoff: async () => {},
    selectTenant: async () => null,
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
    expireSession: () => {},
  };
}

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

const moduleCommands: Command[] = [
  { id: "new-order", label: "New Order", shortcut: "N O", action: () => {} },
  { id: "new-contact", label: "New Contact", shortcut: "N C", action: () => {} },
  { id: "quick-search", label: "Search contacts", shortcut: "Mod+Shift+F", action: () => {} },
];

interface StoryParameters {
  roles?: string[];
  commands?: Command[];
}

// The dialog reads auth, router, module navigation, query client and permissions itself, and
// the command registry is global, so each story registers its own commands
// and removes them on unmount.
const withShell: Decorator = (Story, { parameters }) => {
  const { roles = ["admin"] } = parameters as StoryParameters;
  const queryClient = createQueryClient();
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={fakeAuth(roles)}>
          <PermissionContext.Provider value={permissionValue}>
            <ModuleNavigationProvider navigate={useModuleNavigate()}>
              <Story />
            </ModuleNavigationProvider>
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

const meta: Meta<typeof KeyboardShortcutsDialog> = {
  title: "Chrome/KeyboardShortcutsDialog",
  component: KeyboardShortcutsDialog,
  decorators: [withShell],
  beforeEach: ({ parameters }) => commandRegistry.register((parameters as StoryParameters).commands ?? []),
};

export default meta;

type Story = StoryObj<typeof KeyboardShortcutsDialog>;

async function open(canvasElement: HTMLElement) {
  openKeyboardShortcuts();
  const body = within(canvasElement.ownerDocument.body);
  return waitFor(() => body.getByRole("dialog", { name: "Keyboard shortcuts" }));
}

export const WithModuleShortcuts: Story = {
  parameters: { commands: moduleCommands },
  play: async ({ canvasElement }) => {
    const dialog = await open(canvasElement);
    await expect(within(dialog).getByRole("heading", { name: "Commands" })).toBeInTheDocument();
    await expect(within(dialog).getByText("Go to admin")).toBeInTheDocument();
  },
};

export const NoModuleShortcuts: Story = {
  play: async ({ canvasElement }) => {
    const dialog = await open(canvasElement);
    await expect(within(dialog).queryByRole("heading", { name: "Commands" })).toBeNull();
  },
};

export const NonAdmin: Story = {
  parameters: { roles: [], commands: moduleCommands },
  play: async ({ canvasElement }) => {
    const dialog = await open(canvasElement);
    await expect(within(dialog).queryByText("Go to admin")).toBeNull();
  },
};
