import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useLayoutEffect, useMemo, useState } from "react";
import { AuthContext } from "../auth/auth-provider.js";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import type { AuthContextValue, CurrentTenant, CurrentUser } from "../auth/types.js";
import { AppError } from "../error/app-error.js";
import type { PermissionName } from "../names.js";
import { ModuleNavigationProvider } from "../react/use-module.js";
import type { MetaSchema } from "../schema/types.js";
import { buildEmptyViewRegistry, buildViewRegistry } from "../schema/view-registry.js";
import { ViewRegistryContext } from "../schema/view-registry-provider.js";
import { createMockFetch, type MockRequestHandler } from "./mock-fetch.js";
import { mountTestingSchema } from "./schema-environment.js";
import { mountShellEnvironment } from "./shell-environment.js";

export interface ShellProviderProps {
  module: string;
  user?: Partial<CurrentUser>;
  permissions?: PermissionName[];
  apiMocks?: Record<string, unknown>;
  schema?: MetaSchema;
  requestHandler?: MockRequestHandler;
  children: ReactNode;
}

const DEFAULT_USER: CurrentUser = {
  id: "testing-user",
  email: "user@testing.local",
  name: "Test User",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "light",
  contrast: "standard",
  locale: "en",
  timezone: null,
  dateFormat: null,
};

const TENANT: CurrentTenant = {
  id: "testing-tenant",
  slug: "testing",
  name: "Test Tenant",
  plan: "test",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  defaultCurrency: "",
  availableLocales: ["en"],
  firstDayOfWeek: "monday",
  numberFormat: "1,234.56",
  passwordMinLength: 12,
};

const EMPTY_PERMISSIONS: PermissionName[] = [];
const EMPTY_MOCKS = {};

function unmocked(operation: string): never {
  throw new AppError({
    code: "testing.unmocked_request",
    message: `Unmocked shell operation: ${operation}`,
    httpStatus: 501,
  });
}

function navigate(path: string, options?: { replace?: boolean | undefined }): void {
  if (options?.replace) window.history.replaceState(null, "", path);
  else window.history.pushState(null, "", path);
}

export function ShellProvider({
  module,
  user: inputUser,
  permissions = EMPTY_PERMISSIONS,
  apiMocks = EMPTY_MOCKS,
  schema,
  requestHandler,
  children,
}: ShellProviderProps): ReactNode {
  const user = useMemo(() => ({ ...DEFAULT_USER, ...inputUser }), [inputUser]);
  const mockFetch = useMemo(() => createMockFetch(apiMocks, requestHandler), [apiMocks, requestHandler]);
  const [queryClient] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } }),
  );
  const registry = useMemo(() => (schema ? buildViewRegistry(schema) : buildEmptyViewRegistry()), [schema]);
  const [ready, setReady] = useState(false);

  useLayoutEffect(() => {
    const unmount = mountShellEnvironment({ fetch: mockFetch, user });
    const unmountSchema = mountTestingSchema(schema);
    setReady(true);
    return () => {
      unmountSchema();
      unmount();
    };
  }, [mockFetch, user, schema]);

  useLayoutEffect(() => () => queryClient.clear(), [queryClient]);

  const auth = useMemo<AuthContextValue>(
    () => ({
      state: { status: "authenticated", user, tenant: TENANT },
      isAuthenticated: true,
      user,
      tenant: TENANT,
      login: async () => unmocked("login"),
      completeHandoff: async () => unmocked("completeHandoff"),
      selectTenant: async () => unmocked("selectTenant"),
      logout: async () => unmocked("logout"),
      submitMFA: async () => unmocked("submitMFA"),
      updateProfile: async () => unmocked("updateProfile"),
      updatePreferences: async () => unmocked("updatePreferences"),
      changePassword: async () => unmocked("changePassword"),
      reloadSession: async () => unmocked("reloadSession"),
      expireSession: () => unmocked("expireSession"),
    }),
    [user],
  );
  const permissionContext = useMemo(
    () =>
      createPermissionContextValue({
        permissions: new Set(permissions),
        fieldAccess: {},
        modulesEnabled: new Set([
          module,
          ...Object.keys(schema?.modules ?? {}),
          ...permissions.map((permission) => permission.split(":")[0] ?? module),
        ]),
      }),
    [module, permissions, schema],
  );

  if (!ready) return null;

  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext value={auth}>
        <PermissionContext value={permissionContext}>
          <ViewRegistryContext value={registry}>
            <ModuleNavigationProvider navigate={navigate}>{children}</ModuleNavigationProvider>
          </ViewRegistryContext>
        </PermissionContext>
      </AuthContext>
    </QueryClientProvider>
  );
}
