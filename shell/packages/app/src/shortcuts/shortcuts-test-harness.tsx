import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { vi } from "vitest";
import { CommandPalette } from "../chrome/command-palette.js";
import { GlobalShortcuts } from "./global-shortcuts.js";
import { KeyboardShortcutsDialog } from "./keyboard-shortcuts-dialog.js";

function fakeAuth(roles: string[]): AuthContextValue {
  const user = {
    id: "u1",
    email: "a@b.com",
    contactId: null,
    name: null,
    avatarUrl: null,
    roles,
    amr: [],
    mfaVerifiedAt: null,
    mfaSetupRequired: false,
    theme: "system" as const,
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
    availableLocales: ["en"],
  };
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: vi.fn(),
    completeHandoff: vi.fn(),
    logout: vi.fn(async () => {}),
    submitMFA: vi.fn(),
    updateProfile: vi.fn(),
    updatePreferences: vi.fn(),
    changePassword: vi.fn(),
    reloadSession: vi.fn(),
  };
}

function Page() {
  return (
    <div>
      <input aria-label="Name" />
      <textarea aria-label="Notes" />
      {/* biome-ignore lint/a11y/useSemanticElements: stands in for a rich-text editor's contenteditable surface, which carries role="textbox" the same way (rich-text-field.tsx). */}
      <div role="textbox" aria-label="Editor" tabIndex={0} contentEditable suppressContentEditableWarning />
      <button type="button">Plain button</button>
      <button type="button" role="combobox" aria-expanded={false} aria-controls="none" aria-label="Status">
        Draft
      </button>
    </div>
  );
}

// The root layout's shortcut-related pieces, under routes the shell
// shortcuts navigate to, plus any `extra` chrome under test.
export async function renderShell(
  opts: { roles?: string[]; permissions?: string[]; path?: string; extra?: ReactNode } = {},
) {
  const queryClient = new QueryClient();
  const permissionValue = createPermissionContextValue({
    permissions: new Set(opts.permissions ?? []),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={fakeAuth(opts.roles ?? [])}>
          <PermissionContext.Provider value={permissionValue}>
            <Outlet />
            <CommandPalette />
            <KeyboardShortcutsDialog />
            <GlobalShortcuts />
            {opts.extra}
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const routes = ["/", "/elsewhere", "/settings/profile", "/admin"].map((path) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: Page }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [opts.path ?? "/elsewhere"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return router;
}
