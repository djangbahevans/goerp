import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { useContext, useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MetaSchema } from "../schema/types.js";
import { ViewRegistryContext, ViewRegistryProvider } from "../schema/view-registry-provider.js";
import { sessionIdentity } from "./auth-machine.js";
import { AuthContext } from "./auth-provider.js";
import { PermissionContext, PermissionProvider } from "./permission-provider.js";
import type { PermissionData } from "./permission-types.js";
import type { AuthContextValue, AuthState, CurrentUser } from "./types.js";

const { fetchPermissionsMock, getSchemaMock } = vi.hoisted(() => ({
  fetchPermissionsMock: vi.fn<() => Promise<PermissionData>>(),
  getSchemaMock: vi.fn<() => Promise<MetaSchema>>(),
}));
vi.mock("./permission-client.js", () => ({ fetchPermissions: () => fetchPermissionsMock() }));
vi.mock("../schema/schema-registry.js", () => ({
  schemaRegistry: { getSchema: () => getSchemaMock(), invalidate: () => {} },
}));
vi.mock("../realtime/ws-manager.js", () => ({
  tenantChannel: (tenantId: string) => `tenant:${tenantId}`,
  userChannel: (userId: string) => `ui:user:${userId}`,
  wsManager: { subscribe: () => () => {} },
}));

const user: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  contactId: null,
  name: "Ada",
  avatarUrl: null,
  roles: [],
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
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};
const SCHEMA = {
  modules: {
    sales: {
      name: "sales",
      version: "1.0.0",
      display_name: "Sales",
      routes: [],
      views: [],
      navigation: [],
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      models: {},
      permissions: [],
      frontend: null,
      public_config: {},
    },
  },
} as unknown as MetaSchema;

function authFor(state: AuthState): AuthContextValue {
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  return {
    state,
    isAuthenticated,
    user: sessionIdentity(state)?.user ?? null,
    tenant: sessionIdentity(state)?.tenant ?? null,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
}

const SIGNED_IN = authFor({ status: "authenticated", user, tenant });
const EXPIRED = authFor({ status: "unauthenticated", sessionExpired: true, user, tenant });
const SIGNED_OUT = authFor({ status: "unauthenticated" });

const mounts = { count: 0 };

function Page() {
  const permissions = useContext(PermissionContext);
  const registry = useContext(ViewRegistryContext);
  useEffect(() => {
    mounts.count += 1;
  }, []);
  return (
    <p data-testid="page">
      {permissions?.moduleEnabled("sales") ? "sales enabled" : "sales disabled"} /{" "}
      {registry?.getModuleDisplayName("sales") ?? "no schema"}
    </p>
  );
}

function tree(auth: AuthContextValue) {
  return (
    <AuthContext.Provider value={auth}>
      <PermissionProvider>
        <ViewRegistryProvider>
          <Page />
        </ViewRegistryProvider>
      </PermissionProvider>
    </AuthContext.Provider>
  );
}

beforeEach(() => {
  mounts.count = 0;
  fetchPermissionsMock.mockReset().mockResolvedValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(["sales"]),
  });
  getSchemaMock.mockReset().mockResolvedValue(SCHEMA);
});
afterEach(cleanup);

describe("session expiry under the permission and view-registry providers", () => {
  it("keeps the page mounted with its permissions and schema, without refetching", async () => {
    const { rerender } = render(tree(SIGNED_IN));
    await waitFor(() => expect(screen.getByTestId("page").textContent).toBe("sales enabled / Sales"));

    act(() => rerender(tree(EXPIRED)));

    expect(screen.getByTestId("page").textContent).toBe("sales enabled / Sales");
    expect(mounts.count).toBe(1);
    expect(fetchPermissionsMock).toHaveBeenCalledOnce();
    expect(getSchemaMock).toHaveBeenCalledOnce();
  });

  it("clears them on a sign-out", async () => {
    const { rerender } = render(tree(SIGNED_IN));
    await waitFor(() => expect(screen.getByTestId("page").textContent).toBe("sales enabled / Sales"));

    act(() => rerender(tree(SIGNED_OUT)));

    expect(screen.getByTestId("page").textContent).toBe("sales disabled / no schema");
  });
});
