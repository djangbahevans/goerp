import { AuthContext, type AuthContextValue } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, type ViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import type { ReactNode } from "react";

export function storyAuth(roles: string[]): AuthContextValue {
  const user = {
    id: "u1",
    email: "ada@example.com",
    contactId: null,
    name: "Ada Lovelace",
    avatarUrl: null,
    roles,
    amr: ["pwd"],
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
    name: "Acme Corp",
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
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
}

// Wraps a story: the error pages' links need a router, the 403 page reads
// the signed-in user and the view registry, and the tenant-suspended page
// clears a cached query.
export function ErrorPageStoryProviders({
  auth = storyAuth([]),
  registry = buildEmptyViewRegistry(),
  children,
}: {
  auth?: AuthContextValue | undefined;
  registry?: ViewRegistry | undefined;
  children: ReactNode;
}): ReactNode {
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={new QueryClient()}>
        <AuthContext.Provider value={auth}>
          <ViewRegistryContext.Provider value={registry}>{children}</ViewRegistryContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return <RouterProvider router={router} />;
}
