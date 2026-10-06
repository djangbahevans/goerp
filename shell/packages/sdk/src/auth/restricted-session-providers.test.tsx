import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useViewRegistryStatus, ViewRegistryProvider } from "../schema/view-registry-provider.js";
import { AuthContext } from "./auth-provider.js";
import { PermissionProvider, usePermissionsStatus } from "./permission-provider.js";
import type { AuthContextValue } from "./types.js";

const { permissions, schema } = vi.hoisted(() => ({ permissions: vi.fn(), schema: vi.fn() }));
vi.mock("./permission-client.js", () => ({ fetchPermissions: permissions }));
vi.mock("../schema/schema-registry.js", () => ({ schemaRegistry: { getSchema: schema, invalidate: () => {} } }));
vi.mock("../realtime/ws-manager.js", () => ({
  tenantChannel: (id: string) => id,
  userChannel: (id: string) => id,
  wsManager: { subscribe: () => () => {} },
}));

afterEach(cleanup);

it("defers workspace data until the password restriction clears and hides it when restriction returns", async () => {
  permissions.mockResolvedValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() });
  schema.mockResolvedValue({ modules: {} });
  const user = {
    id: "u1",
    email: "a@example.com",
    contactId: null,
    name: null,
    avatarUrl: null,
    roles: [],
    amr: [],
    mfaVerifiedAt: null,
    mfaSetupRequired: false,
    passwordChangeRequired: true,
    passwordMinLength: 18,
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
    availableLocales: ["en"],
    firstDayOfWeek: "monday" as const,
    numberFormat: "1,234.56" as const,
    passwordMinLength: 18,
  };
  const auth: AuthContextValue = {
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
  function Status() {
    return (
      <p>
        {usePermissionsStatus()}:{useViewRegistryStatus()}
      </p>
    );
  }
  function ui(value: AuthContextValue) {
    return (
      <AuthContext.Provider value={value}>
        <PermissionProvider>
          <ViewRegistryProvider>
            <Status />
          </ViewRegistryProvider>
        </PermissionProvider>
      </AuthContext.Provider>
    );
  }
  const { rerender } = render(ui(auth));
  expect(screen.getByText("loading:loading")).toBeTruthy();
  expect(permissions).not.toHaveBeenCalled();
  expect(schema).not.toHaveBeenCalled();

  const unlocked = { ...user, passwordChangeRequired: false };
  rerender(ui({ ...auth, user: unlocked, state: { status: "authenticated", user: unlocked, tenant } }));
  await waitFor(() => expect(screen.getByText("ready:ready")).toBeTruthy());
  expect(permissions).toHaveBeenCalledTimes(1);
  expect(schema).toHaveBeenCalledTimes(1);

  rerender(ui(auth));
  expect(screen.getByText("loading:loading")).toBeTruthy();
  expect(permissions).toHaveBeenCalledTimes(1);
  expect(schema).toHaveBeenCalledTimes(1);
});
