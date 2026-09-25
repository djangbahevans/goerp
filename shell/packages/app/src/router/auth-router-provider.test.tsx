import type { AuthContextValue, AuthState } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { createRef, type ReactNode, type RefObject, useImperativeHandle, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthRouterProvider } from "./auth-router-provider.js";
import { routeTree } from "./routeTree.gen.js";

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
};
const AUTHENTICATED: AuthState = { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT };

type SetAuthState = (state: AuthState) => void;

// Exposes setState through a ref so a test can drive auth transitions from
// outside, the way the real authMachine changes under AuthProvider.
function FakeAuthProvider({
  initial,
  control,
  children,
}: {
  initial: AuthState;
  control: RefObject<SetAuthState | null>;
  children: ReactNode;
}) {
  const [state, setState] = useState<AuthState>(initial);
  useImperativeHandle(control, () => setState, []);
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  const value: AuthContextValue = {
    state,
    isAuthenticated,
    user: isAuthenticated ? state.user : null,
    tenant: isAuthenticated ? state.tenant : null,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

function renderAt(path: string, initial: AuthState) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ tenant: null, registration_enabled: false }), { status: 200 })),
  );
  const router = createRouter({
    routeTree,
    context: { auth: undefined! },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  const control = createRef<SetAuthState>();
  const setAuthState: SetAuthState = (next) => act(() => control.current?.(next));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider initial={initial} control={control}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  return { router, setAuthState };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("auth gate", () => {
  it("redirects an unauthenticated visitor to sign in, keeping the full original URL", async () => {
    const { router } = renderAt("/settings/profile?tab=security", { status: "unauthenticated" });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile?tab=security" });
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("gates the index route too", async () => {
    const { router } = renderAt("/", { status: "unauthenticated" });
    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
  });

  it("never gates /auth/login", async () => {
    const { router } = renderAt("/auth/login?notice=mfa_failed", { status: "unauthenticated" });

    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(router.state.location.href).toBe("/auth/login?notice=mfa_failed");
  });

  it("never gates /auth/mfa — its own page handles a visit with no challenge", async () => {
    const { router } = renderAt("/auth/mfa?redirect=%2Fsettings%2Fprofile", { status: "unauthenticated" });

    // The MFA page's redirect keeps the original target; the gate's would
    // have replaced it with /auth/mfa itself.
    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("lets an authenticated visitor through", async () => {
    const { router } = renderAt("/settings/profile", AUTHENTICATED);

    expect(await screen.findByRole("heading", { name: "Profile" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/settings/profile");
  });

  it("renders nothing until the session check settles, then admits a signed-in reload", async () => {
    const { router, setAuthState } = renderAt("/settings/profile", { status: "checking" });

    expect(screen.queryByRole("heading")).toBeNull();
    setAuthState(AUTHENTICATED);

    expect(await screen.findByRole("heading", { name: "Profile" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/settings/profile");
  });

  it("redirects to sign in once the session check comes back empty", async () => {
    const { router, setAuthState } = renderAt("/settings/profile", { status: "checking" });

    setAuthState({ status: "unauthenticated" });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("survives a router invalidation while the session check is still pending", async () => {
    // ViewRegistryProvider invalidates the router on its own schedule, which
    // can land before the provider has rendered it.
    const { router, setAuthState } = renderAt("/settings/profile", { status: "checking" });

    await act(() => router.invalidate());
    setAuthState({ status: "unauthenticated" });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("redirects to sign in when the session ends while on a protected page", async () => {
    const { router, setAuthState } = renderAt("/settings/profile", AUTHENTICATED);
    await screen.findByRole("heading", { name: "Profile" });

    setAuthState({ status: "unauthenticated" });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("moves to MFA setup when a request flags the user mid-session", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ enrollment_id: "e1", qr_svg: "<svg/>", secret: "ABCD" }))),
    );
    const { router, setAuthState } = renderAt("/settings/profile", AUTHENTICATED);
    await screen.findByRole("heading", { name: "Profile" });

    setAuthState({ status: "authenticated", user: { ...FAKE_USER, mfaSetupRequired: true }, tenant: FAKE_TENANT });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa-setup"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
    vi.unstubAllGlobals();
  });

  it("re-runs the gate exactly once when the session check settles", async () => {
    const { router, setAuthState } = renderAt("/settings/profile", { status: "checking" });
    const invalidate = vi.spyOn(router, "invalidate");

    setAuthState(AUTHENTICATED);
    await screen.findByRole("heading", { name: "Profile" });

    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("doesn't re-run route loading on a token refresh", async () => {
    const { router, setAuthState } = renderAt("/settings/profile", AUTHENTICATED);
    await screen.findByRole("heading", { name: "Profile" });
    const invalidate = vi.spyOn(router, "invalidate");

    setAuthState({ status: "refreshing", user: FAKE_USER, tenant: FAKE_TENANT });
    setAuthState(AUTHENTICATED);

    expect(invalidate).not.toHaveBeenCalled();
  });

  it("treats a pending MFA challenge as not signed in on a protected route", async () => {
    const { router } = renderAt("/settings/profile", {
      status: "mfa_required",
      challengeToken: "tok",
      methods: ["totp"],
    });
    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
  });
});
