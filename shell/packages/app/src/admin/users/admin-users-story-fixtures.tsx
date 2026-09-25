import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import {
  type FakeBackendOptions,
  type FakeSession,
  type FakeUser,
  installFakeAdminUsersBackend,
} from "./fake-admin-users-backend.js";

const me = {
  id: "me",
  email: "ada@acme.test",
  name: "Ada Admin",
  contactId: null,
  avatarUrl: null,
  roles: ["admin"],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
};
const tenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

const auth: AuthContextValue = {
  state: { status: "authenticated", user: me, tenant },
  isAuthenticated: true,
  user: me,
  tenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const ago = (hours: number) => new Date(Date.now() - hours * 3600 * 1000).toISOString();

export const STORY_USERS: FakeUser[] = [
  { id: "me", name: "Ada Admin", email: "ada@acme.test", roles: ["admin"], status: "active", lastLoginAt: ago(0.2) },
  {
    id: "u-bola",
    name: "Bola Mensah",
    email: "bola.mensah@acme.test",
    roles: ["portal", "user"],
    status: "active",
    lastLoginAt: ago(5),
    phone: "+233 20 555 0142",
  },
  {
    id: "u-chidi",
    name: "Chidi Okafor",
    email: "chidi.okafor@acme.test",
    roles: ["user"],
    status: "suspended",
    lastLoginAt: ago(24 * 12),
  },
  {
    id: "u-efua",
    name: "Efua Boateng",
    email: "efua.boateng@acme.test",
    roles: [],
    status: "invited",
    lastLoginAt: null,
    invitation: { id: "inv-efua", role: "user", expiresAt: ago(-24 * 5), createdAt: ago(48) },
  },
  { id: "u-kwame", name: null, email: "kwame@acme.test", roles: ["user"], status: "active", lastLoginAt: null },
];

export const STORY_SESSIONS: Record<string, FakeSession[]> = {
  "u-bola": [
    {
      id: "fam-laptop",
      userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
      ipAddress: "41.66.18.2",
      countryCode: "GH",
      signedInAt: ago(72),
      lastActiveAt: ago(1),
    },
    {
      id: "fam-phone",
      userAgent: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/128.0.0.0 Mobile Safari/537.36",
      ipAddress: "102.176.4.9",
      countryCode: "GH",
      signedInAt: ago(200),
      lastActiveAt: ago(30),
    },
  ],
};

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeBackend(options: Partial<FakeBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminUsersBackend({ users: STORY_USERS, sessions: STORY_SESSIONS, ...options });
    return backend.restore;
  };
}

// Auth as the tenant admin "me", a router for navigation hooks, and a fresh
// query client per story so one story's cache can't leak into the next.
export const withAdminShell: Decorator = (Story) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider value={permissions}>
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
