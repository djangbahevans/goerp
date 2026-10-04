import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, tenantSuspension } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
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
const SIGNED_IN: AuthContextValue = {
  state: { status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT },
  isAuthenticated: true,
  user: FAKE_USER,
  tenant: FAKE_TENANT,
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
const SIGNED_OUT: AuthContextValue = {
  ...SIGNED_IN,
  state: { status: "unauthenticated" },
  isAuthenticated: false,
  user: null,
  tenant: null,
};
const EXPIRED: AuthContextValue = {
  ...SIGNED_OUT,
  state: { status: "unauthenticated", sessionExpired: true, user: FAKE_USER, tenant: FAKE_TENANT },
  user: FAKE_USER,
  tenant: FAKE_TENANT,
};

function mount(path: string, auth: AuthContextValue) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createRouter({ routeTree, context: { auth }, history });
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const ui = (value: AuthContextValue) => (
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={value}>
        <PermissionContext.Provider value={permissions}>
          <ViewRegistryContext.Provider value={buildEmptyViewRegistry()}>
            <AuthRouterProvider router={router} />
          </ViewRegistryContext.Provider>
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
  const { rerender } = render(ui(auth));
  return { router, setAuth: (value: AuthContextValue) => rerender(ui(value)) };
}

function renderAt(path: string, auth: AuthContextValue) {
  return mount(path, auth).router;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  tenantSuspension.set(false);
});

const hasChrome = () => screen.queryByRole("navigation", { name: "Main" }) !== null;

describe("root layout", () => {
  it("renders an app route inside the chrome", async () => {
    renderAt("/", SIGNED_IN);

    expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();
    expect(screen.getByRole("banner")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Skip to content" })).toBeTruthy();
  });

  it("holds a user who must enroll in MFA on the setup wizard", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ enrollment_id: "e1", qr_svg: "<svg/>", secret: "ABCD" }))),
    );
    const needsSetup = { ...FAKE_USER, mfaSetupRequired: true };
    const router = renderAt("/settings/profile?tab=a", {
      ...SIGNED_IN,
      state: { status: "authenticated", user: needsSetup, tenant: FAKE_TENANT },
      user: needsSetup,
    });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa-setup"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile?tab=a" });
    expect(await screen.findByRole("heading", { name: /requires two-factor authentication/ })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  it("renders an /auth/* route with no sidebar or header", async () => {
    renderAt("/auth/login", SIGNED_OUT);

    expect(await screen.findByRole("button", { name: /sign in/i })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
    expect(screen.queryByRole("banner")).toBeNull();
    expect(screen.queryByRole("link", { name: "Skip to content" })).toBeNull();
  });

  it("renders an unmatched URL as the 404 page in place, with no chrome", async () => {
    const router = renderAt("/no/such/page", SIGNED_IN);

    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/no/such/page");
    expect(hasChrome()).toBe(false);
  });

  it.each([
    ["/404", "Page not found"],
    ["/403", "You don't have access to this page"],
  ])("renders %s with no chrome", async (path, heading) => {
    renderAt(path, SIGNED_IN);

    expect(await screen.findByRole("heading", { name: heading })).toBeTruthy();
    expect(hasChrome()).toBe(false);
    expect(screen.queryByRole("banner")).toBeNull();
  });

  it("sends a signed-out visitor to the tenant-suspended page once a 403 tenant_suspended arrives", async () => {
    const router = renderAt("/auth/login", SIGNED_OUT);
    expect(await screen.findByRole("button", { name: /sign in/i })).toBeTruthy();

    act(() => tenantSuspension.set(true));

    await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
    expect(await screen.findByRole("heading", { name: "This organisation's account has been suspended" })).toBeTruthy();
    expect(hasChrome()).toBe(false);
  });

  it("holds a signed-in user of a suspended tenant on the tenant-suspended page", async () => {
    tenantSuspension.set(true);
    const router = renderAt("/settings/profile", SIGNED_IN);

    await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
    expect(hasChrome()).toBe(false);
  });

  describe("session expired", () => {
    const expiredDialog = () => screen.queryByRole("alertdialog", { name: "Your session has expired" });

    it("shows the modal over the current page, with no navigation, when the session expires", async () => {
      const { router, setAuth } = mount("/", SIGNED_IN);
      expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();

      act(() => setAuth(EXPIRED));

      expect(await screen.findByRole("alertdialog", { name: "Your session has expired" })).toBeTruthy();
      expect(screen.getByText("Please sign in again to continue.")).toBeTruthy();
      expect(router.state.location.pathname).toBe("/");
      // The page is still there behind the modal, hidden from assistive tech.
      expect(screen.getByRole("navigation", { name: "Main", hidden: true })).toBeTruthy();
      expect(hasChrome()).toBe(false);
      // Queries behind the modal stop refetching on focus and polling.
      expect(focusManager.isFocused()).toBe(false);
    });

    it("can't be dismissed and keeps focus inside the modal", async () => {
      renderAt("/", EXPIRED);
      const dialog = await screen.findByRole("alertdialog", { name: "Your session has expired" });
      await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));

      fireEvent.keyDown(dialog, { key: "Escape" });
      fireEvent.pointerDown(document.body);

      expect(expiredDialog()).toBeTruthy();
      expect(screen.queryByRole("button", { name: /close|cancel/i })).toBeNull();
    });

    it("signs in again at the login page, carrying the page it was shown over", async () => {
      const router = renderAt("/?tab=open", EXPIRED);

      fireEvent.click(await screen.findByRole("button", { name: "Sign in again" }));

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(router.state.location.search).toEqual({ redirect: "/?tab=open" });
      await waitFor(() => expect(expiredDialog()).toBeNull());
      expect(focusManager.isFocused()).toBe(true);
    });

    it("redirects a signed-out visitor to login with no modal", async () => {
      const router = renderAt("/", SIGNED_OUT);

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("redirects to login with no modal after signing out", async () => {
      const { router, setAuth } = mount("/", SIGNED_IN);
      expect(await screen.findByRole("navigation", { name: "Main" })).toBeTruthy();

      act(() => setAuth(SIGNED_OUT));

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("sends a session that expires on an auth page to login instead of holding it there", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(JSON.stringify({ enrollment_id: "e1", qr_svg: "<svg/>", secret: "ABCD" }))),
      );
      const needsSetup = { ...FAKE_USER, mfaSetupRequired: true };
      const { router, setAuth } = mount("/auth/mfa-setup", {
        ...SIGNED_IN,
        state: { status: "authenticated", user: needsSetup, tenant: FAKE_TENANT },
        user: needsSetup,
      });
      expect(await screen.findByRole("heading", { name: /requires two-factor authentication/ })).toBeTruthy();

      act(() =>
        setAuth({
          ...EXPIRED,
          state: { status: "unauthenticated", sessionExpired: true, user: needsSetup, tenant: FAKE_TENANT },
          user: needsSetup,
        }),
      );

      await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
      expect(expiredDialog()).toBeNull();
    });

    it("leaves a suspended tenant's page to its own sign-in flow", async () => {
      tenantSuspension.set(true);
      const router = renderAt("/", EXPIRED);

      await waitFor(() => expect(router.state.location.pathname).toBe("/tenant-suspended"));
      expect(await screen.findByRole("button", { name: "Sign in with a different account" })).toBeTruthy();
      expect(expiredDialog()).toBeNull();
    });
  });
});

describe("password-change restrictions", () => {
  const user = { ...FAKE_USER, passwordChangeRequired: true, passwordMinLength: 18 };
  const restricted: AuthContextValue = {
    ...SIGNED_IN,
    user,
    state: { status: "authenticated", user, tenant: FAKE_TENANT },
  };

  it.each([
    ["/activities?source=auth#events", "/activities?source=auth#events"],
    ["/auth/login?redirect=%2Factivities", "/activities"],
    ["/auth/mfa?redirect=%2Factivities", "/activities"],
    ["/auth/handoff?code=secret&redirect=%2Factivities", "/activities"],
    ["/settings/profile", "/"],
    ["/403", "/403"],
  ])("holds %s on the password card and preserves its destination", async (path, onward) => {
    const router = renderAt(path, restricted);
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(router.state.location.hash).toBe("change-password");
    expect(router.state.location.search).toEqual({ redirect: onward });
    expect(await screen.findByRole("heading", { name: /Change your password to keep using Acme Corp/ })).toBeTruthy();
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByLabelText("Full name")).toBeNull();
    expect(screen.getByText("It needs at least 18 characters.")).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Current password")));
  });

  it("requires a password change before enrolling in MFA", async () => {
    const both = { ...user, mfaSetupRequired: true };
    const router = renderAt("/activities", {
      ...restricted,
      user: both,
      state: { status: "authenticated", user: both, tenant: FAKE_TENANT },
    });
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(router.state.location.search).toEqual({ redirect: "/activities" });
  });

  it("redirects immediately when a live session becomes restricted", async () => {
    const { router, setAuth } = mount("/settings/profile", SIGNED_IN);
    await screen.findByLabelText("Full name");
    setAuth(restricted);
    await screen.findByRole("heading", { name: /Change your password to keep using/ });
    await waitFor(() => expect(router.state.location.hash).toBe("change-password"));
    expect(screen.queryByLabelText("Full name")).toBeNull();
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("returns to the intended page after changing the password and reloading the session", async () => {
    const changePassword = vi.fn(async () => {});
    const reloadSession = vi.fn(async () => setAuth(SIGNED_IN));
    const { router, setAuth } = mount("/settings/profile?redirect=%2Fsettings%2Fappearance#change-password", {
      ...restricted,
      changePassword,
      reloadSession,
    });
    await screen.findByLabelText("Current password");
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old password" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "a new long passphrase" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "a new long passphrase" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/appearance"));
    expect(changePassword).toHaveBeenCalledTimes(1);
    expect(reloadSession).toHaveBeenCalledTimes(1);
  });

  it("retries a failed session reload without submitting the password again", async () => {
    const changePassword = vi.fn(async () => {});
    const reloadSession = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockImplementationOnce(async () => setAuth(SIGNED_IN));
    const { router, setAuth } = mount("/settings/profile?redirect=%2Fsettings%2Fappearance#change-password", {
      ...restricted,
      changePassword,
      reloadSession,
    });
    await screen.findByLabelText("Current password");
    for (const label of ["Current password", "New password", "Confirm new password"]) {
      fireEvent.change(screen.getByLabelText(label), { target: { value: "a new long passphrase" } });
    }
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Your password is updated");
    expect(screen.queryByLabelText("Current password")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/appearance"));
    expect(changePassword).toHaveBeenCalledTimes(1);
    expect(reloadSession).toHaveBeenCalledTimes(2);
  });

  it("signs out from restricted mode", async () => {
    const logout = vi.fn(async () => setAuth(SIGNED_OUT));
    const { router, setAuth } = mount("/settings/profile#change-password", { ...restricted, logout });
    await screen.findByRole("button", { name: "Sign out" });
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(logout).toHaveBeenCalledTimes(1);
  });
});

it("lets an expired restricted session sign in again while preserving its original destination", async () => {
  const user = { ...FAKE_USER, passwordChangeRequired: true };
  const expired: AuthContextValue = {
    ...SIGNED_OUT,
    user,
    tenant: FAKE_TENANT,
    state: { status: "unauthenticated", sessionExpired: true, user, tenant: FAKE_TENANT },
  };
  const router = renderAt("/settings/profile?redirect=%2Factivities#change-password", expired);
  await screen.findByRole("alertdialog");
  fireEvent.click(screen.getByRole("button", { name: "Sign in again" }));
  await screen.findByRole("heading", { name: "Sign in" });
  expect(router.state.location.search).toEqual({ redirect: "/activities" });
  expect(screen.queryByLabelText("Current password")).toBeNull();
});
