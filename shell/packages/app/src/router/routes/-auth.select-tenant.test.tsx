import type { AuthContextValue, AuthState } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { pendingTenantSelection } from "../../auth/tenant-selection.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const FAKE_USER = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
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
const FAKE_TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme Corp",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  passwordMinLength: 12,
};

type SelectImpl = (
  token: string,
  tenant: string,
  setState: (state: AuthState) => void,
) => Promise<{ host: string; code: string } | null>;

function FakeAuthProvider({ selectImpl, children }: { selectImpl: SelectImpl; children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "unauthenticated" });
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  const value: AuthContextValue = {
    state,
    isAuthenticated,
    user: isAuthenticated ? state.user : null,
    tenant: isAuthenticated ? state.tenant : null,
    login: async () => null,
    completeHandoff: async () => {},
    selectTenant: (token, tenant) => selectImpl(token, tenant, setState),
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
    expireSession: () => {},
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

const SELECTION = {
  tenants: [
    { slug: "acme", name: "Acme Corp" },
    { slug: "globex", name: "Globex" },
  ],
  selectionToken: "tok",
};

async function renderSelect(url: string, selectImpl: SelectImpl) {
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = createRouter({ routeTree, context: { auth: undefined! }, history });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider selectImpl={selectImpl}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  return router;
}

afterEach(() => {
  cleanup();
  pendingTenantSelection.set(null);
  vi.unstubAllGlobals();
});

describe("/auth/select-tenant", () => {
  it("sends a visitor with no pending selection back to sign in", async () => {
    const router = await renderSelect("/auth/select-tenant", vi.fn<SelectImpl>());

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
  });

  it("lists the tenants and signs in to the one picked, then goes to ?redirect", async () => {
    pendingTenantSelection.set(SELECTION);
    const selectImpl = vi.fn<SelectImpl>(async (_token, _tenant, setState) => {
      setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT });
      return null;
    });
    const router = await renderSelect("/auth/select-tenant?redirect=%2Fsettings%2Fprofile", selectImpl);

    const heading = await screen.findByRole("heading", { name: "Choose an organisation" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(screen.getByRole("button", { name: /Acme Corp/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Globex/ }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(selectImpl).toHaveBeenCalledWith("tok", "globex", expect.any(Function));
    expect(pendingTenantSelection.get()).toBeNull();
  });

  it("leaves for the chosen tenant's host on a handoff", async () => {
    pendingTenantSelection.set(SELECTION);
    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, protocol: "http:", port: "5173", assign });
    await renderSelect("/auth/select-tenant", async () => ({ host: "acme.localhost", code: "c0de" }));

    fireEvent.click(await screen.findByRole("button", { name: /Acme Corp/ }));

    await waitFor(() => expect(assign).toHaveBeenCalledWith("http://acme.localhost:5173/auth/handoff?code=c0de"));
  });

  it("goes to the MFA challenge when the pick asks for a second factor", async () => {
    pendingTenantSelection.set(SELECTION);
    const router = await renderSelect("/auth/select-tenant", async (_token, _tenant, setState) => {
      setState({ status: "mfa_required", challengeToken: "mfa", methods: ["totp"] });
      return null;
    });

    fireEvent.click(await screen.findByRole("button", { name: /Acme Corp/ }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa"));
  });

  it("starts the sign-in over when the token is spent or expired", async () => {
    pendingTenantSelection.set(SELECTION);
    const router = await renderSelect("/auth/select-tenant", async () => {
      throw new AppError({ code: "auth.selection_token_invalid", message: "expired", httpStatus: 401 });
    });

    fireEvent.click(await screen.findByRole("button", { name: /Acme Corp/ }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ notice: "session_failed" });
    expect(pendingTenantSelection.get()).toBeNull();
  });

  it("keeps the choices with an error when the request fails outright", async () => {
    pendingTenantSelection.set(SELECTION);
    await renderSelect("/auth/select-tenant", async () => {
      throw new TypeError("network");
    });

    fireEvent.click(await screen.findByRole("button", { name: /Acme Corp/ }));

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect((screen.getByRole("button", { name: /Globex/ }) as HTMLButtonElement).disabled).toBe(false);
  });
});
