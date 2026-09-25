import type { AuthContextValue, AuthState, LoginCredentials } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { type ReactNode, useState } from "react";
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

type LoginImpl = (credentials: LoginCredentials, setState: (state: AuthState) => void) => Promise<void>;

// A stateful stand-in for AuthProvider, so each test drives its own auth
// state instead of sharing the process-wide authMachine singleton.
function FakeAuthProvider({
  initial,
  loginImpl,
  children,
}: {
  initial: AuthState;
  loginImpl: LoginImpl;
  children: ReactNode;
}) {
  const [state, setState] = useState<AuthState>(initial);
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  const value: AuthContextValue = {
    state,
    isAuthenticated,
    user: isAuthenticated ? state.user : null,
    tenant: isAuthenticated ? state.tenant : null,
    login: (credentials) => loginImpl(credentials, setState),
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

function stubTenantContext(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url === "/auth/tenant-context") return new Response(JSON.stringify(body), { status: 200 });
      return new Response(null, { status: 404 });
    }),
  );
}

const SUBDOMAIN_TENANT = { tenant: { slug: "acme", name: "Acme Corp" }, registration_enabled: false };

async function renderLogin({
  url = "/auth/login",
  initial = { status: "unauthenticated" } as AuthState,
  loginImpl = vi.fn<LoginImpl>(async () => {}),
}: {
  url?: string;
  initial?: AuthState;
  loginImpl?: LoginImpl;
} = {}) {
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = createRouter({ routeTree, context: { auth: undefined! }, history });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider initial={initial} loginImpl={loginImpl}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  const submit = await screen.findByRole("button", { name: "Sign in" });
  await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
  return { router, history, loginImpl, submit };
}

function fillCredentials(email = "ada@example.com", password = "hunter22") {
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: email } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: password } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("/auth/login", () => {
  it("sends the subdomain-resolved tenant and remember choice, then redirects to ?redirect", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const loginImpl = vi.fn<LoginImpl>(async (_credentials, setState) => {
      setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT });
    });
    const { router, submit } = await renderLogin({ url: "/auth/login?redirect=%2Fsettings%2Fprofile", loginImpl });

    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.queryByLabelText("Company")).toBeNull();

    fillCredentials();
    fireEvent.click(screen.getByLabelText("Remember this device"));
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(loginImpl).toHaveBeenCalledWith(
      { email: "ada@example.com", password: "hunter22", tenant: "acme", remember: true },
      expect.any(Function),
    );
  });

  it("redirects to / when no redirect param is given", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { router, submit } = await renderLogin({
      loginImpl: async (_c, setState) => setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT }),
    });

    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("ignores an off-origin redirect param", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { router, submit } = await renderLogin({
      url: "/auth/login?redirect=%2F%2Fevil.example",
      loginImpl: async (_c, setState) => setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT }),
    });

    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("hands off to the MFA challenge on mfa_required, keeping the redirect", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { router, submit } = await renderLogin({
      url: "/auth/login?redirect=%2Fsettings%2Fprofile",
      loginImpl: async (_c, setState) => setState({ status: "mfa_required", challengeToken: "tok", methods: ["totp"] }),
    });

    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.href).toBe("/auth/mfa?redirect=%2Fsettings%2Fprofile"));
  });

  it("lets a user who backed out of the MFA challenge sign in again without bouncing back to it", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { router, submit } = await renderLogin({
      initial: { status: "mfa_required", challengeToken: "stale", methods: ["totp"] },
      loginImpl: async (_c, setState) => setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT }),
    });

    expect(router.state.location.pathname).toBe("/auth/login");
    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("shows an MFA notice until the next submit", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { submit } = await renderLogin({ url: "/auth/login?notice=mfa_failed" });

    expect(screen.getByText("Incorrect or expired code. Sign in again.")).toBeTruthy();
    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(screen.queryByText("Incorrect or expired code. Sign in again.")).toBeNull());
  });

  it("shows the email_verified notice as a success status, not an alert", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    await renderLogin({ url: "/auth/login?notice=email_verified" });

    const notice = screen.getByText("Email verified. Sign in to continue.");
    expect(notice.getAttribute("role")).toBe("status");
    expect(notice.className).toContain("text-success");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ignores an unknown notice value", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    await renderLogin({ url: "/auth/login?notice=%3Cscript%3E" });

    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows the Company field on a shared domain and sends its slug", async () => {
    stubTenantContext({ tenant: null, registration_enabled: false });
    const loginImpl = vi.fn<LoginImpl>(async () => {});
    const { submit } = await renderLogin({ loginImpl });

    fillCredentials();
    fireEvent.click(submit);
    expect(await screen.findByText("Enter your company.")).toBeTruthy();
    expect(loginImpl).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Company"), { target: { value: " globex " } });
    fireEvent.click(submit);

    await waitFor(() =>
      expect(loginImpl).toHaveBeenCalledWith(expect.objectContaining({ tenant: "globex" }), expect.any(Function)),
    );
  });

  it("validates empty fields without calling the API", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const loginImpl = vi.fn<LoginImpl>(async () => {});
    const { submit } = await renderLogin({ loginImpl });

    fireEvent.click(submit);

    expect(await screen.findByText("Enter your email address.")).toBeTruthy();
    expect(screen.getByText("Enter your password.")).toBeTruthy();
    expect(loginImpl).not.toHaveBeenCalled();
  });

  it("on invalid credentials shows the inline error, clears the password, and focuses email", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { submit } = await renderLogin({
      loginImpl: async () => {
        throw new AppError({ code: "invalid_credentials", message: "invalid email or password", httpStatus: 401 });
      },
    });

    fillCredentials();
    fireEvent.click(submit);

    expect(await screen.findByText("Invalid email or password")).toBeTruthy();
    expect((screen.getByLabelText("Password") as HTMLInputElement).value).toBe("");
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Email")));
  });

  it("on 429 shows a live countdown, disables the form, and re-enables it when the countdown ends", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { submit } = await renderLogin({
      loginImpl: async () => {
        throw new AppError({
          code: "rate_limit_exceeded",
          message: "too many requests",
          httpStatus: 429,
          details: { retryAfter: 3 },
        });
      },
    });

    fillCredentials();
    vi.useFakeTimers({ shouldAdvanceTime: false, toFake: ["setInterval", "clearInterval", "Date"] });
    await act(async () => {
      fireEvent.click(submit);
    });

    const status = screen.getByRole("status");
    expect(status.textContent).toBe("Too many attempts. Try again in 3 seconds.");
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText("Email") as HTMLInputElement).disabled).toBe(true);

    await act(async () => {
      vi.advanceTimersByTime(1200);
    });
    expect(status.textContent).toBe("Too many attempts. Try again in 2 seconds.");

    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(status.textContent).toBe("");
    expect((submit as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the email-verification message for email_verification_required", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { submit } = await renderLogin({
      loginImpl: async () => {
        throw new AppError({ code: "email_verification_required", message: "", httpStatus: 403 });
      },
    });

    fillCredentials();
    fireEvent.click(submit);

    expect(await screen.findByText(/Verify your email address before signing in/)).toBeTruthy();
  });

  it("resends the verification email for the failed sign-in's email and tenant, then cools down for 60 seconds", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url === "/auth/tenant-context") return new Response(JSON.stringify(SUBDOMAIN_TENANT), { status: 200 });
      if (url === "/auth/verify-email/resend") return new Response(JSON.stringify({ status: "ok" }), { status: 200 });
      return new Response(null, { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const { submit } = await renderLogin({
      loginImpl: async () => {
        throw new AppError({ code: "email_verification_required", message: "", httpStatus: 403 });
      },
    });

    fillCredentials();
    fireEvent.click(submit);
    const resend = await screen.findByRole("button", { name: "Resend verification email" });
    // The resend targets the sign-in that failed, not whatever the field holds now.
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "" } });

    vi.useFakeTimers({ shouldAdvanceTime: false, toFake: ["setInterval", "clearInterval", "Date"] });
    await act(async () => {
      fireEvent.click(resend);
    });

    const status = screen.getByText(/If your account still needs verifying/);
    expect(status.getAttribute("role")).toBe("status");
    expect(status.textContent).toBe(
      "If your account still needs verifying, a new link is on its way. Resend again in 60 seconds.",
    );
    expect(screen.queryByRole("button", { name: "Resend verification email" })).toBeNull();
    const resendCall = fetchMock.mock.calls.find(([url]) => url === "/auth/verify-email/resend") as
      | [string, RequestInit]
      | undefined;
    expect(JSON.parse(String(resendCall?.[1].body))).toEqual({ email: "ada@example.com", tenant: "acme" });

    await act(async () => {
      vi.advanceTimersByTime(60_200);
    });
    expect(status.textContent).toBe("If your account still needs verifying, a new link is on its way.");
    expect(screen.getByRole("button", { name: "Resend verification email" })).toBeTruthy();
  });

  it("offers no resend for other login errors", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { submit } = await renderLogin({
      loginImpl: async () => {
        throw new AppError({ code: "invalid_credentials", message: "", httpStatus: 401 });
      },
    });

    fillCredentials();
    fireEvent.click(submit);

    expect(await screen.findByText("Invalid email or password")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Resend verification email" })).toBeNull();
  });

  it("sends a user who must enroll in MFA to the setup wizard, keeping ?redirect", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const { router, submit } = await renderLogin({
      url: "/auth/login?redirect=%2Fsettings%2Fprofile",
      loginImpl: async (_c, setState) =>
        setState({ status: "authenticated", user: { ...FAKE_USER, mfaSetupRequired: true }, tenant: FAKE_TENANT }),
    });

    fillCredentials();
    fireEvent.click(submit);

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa-setup"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("shows 'Create an account' only when registration is enabled", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    await renderLogin();
    expect(screen.queryByRole("link", { name: "Create an account" })).toBeNull();
    cleanup();

    stubTenantContext({ ...SUBDOMAIN_TENANT, registration_enabled: true });
    await renderLogin();
    expect(screen.getByRole("link", { name: "Create an account" }).getAttribute("href")).toBe("/auth/register");
  });

  it("redirects an already-authenticated visitor straight through", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const history = createMemoryHistory({ initialEntries: ["/auth/login?redirect=%2Fsettings%2Fprofile"] });
    const router = createRouter({ routeTree, context: { auth: undefined! }, history });
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <FakeAuthProvider
          initial={{ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT }}
          loginImpl={async () => {}}
        >
          <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
            <AuthRouterProvider router={router} />
          </PermissionContext.Provider>
        </FakeAuthProvider>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
  });

  it("keeps submit disabled while a logout is still in flight", async () => {
    stubTenantContext(SUBDOMAIN_TENANT);
    const history = createMemoryHistory({ initialEntries: ["/auth/login"] });
    const router = createRouter({ routeTree, context: { auth: undefined! }, history });
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <FakeAuthProvider initial={{ status: "logging_out" }} loginImpl={async () => {}}>
          <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
            <AuthRouterProvider router={router} />
          </PermissionContext.Provider>
        </FakeAuthProvider>
      </QueryClientProvider>,
    );

    const submit = await screen.findByRole("button", { name: "Sign in" });
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeTruthy());
    expect((submit as HTMLButtonElement).disabled).toBe(true);
  });
});
