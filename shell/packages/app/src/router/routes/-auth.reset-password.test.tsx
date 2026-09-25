import {
  AuthContext,
  type AuthContextValue,
  createPermissionContextValue,
  PermissionContext,
  permissionDataRef,
} from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ResetPasswordPage } from "../../auth/reset-password-page.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const SIGNED_OUT: AuthContextValue = {
  state: { status: "unauthenticated" },
  isAuthenticated: false,
  user: null,
  tenant: null,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const STRONG = "Plinth-Quartz-Meadow-47";
// 12+ characters but zxcvbn score 0.
const WEAK = "password1234";
const EXPIRED_TEXT = "This reset link has expired or has already been used. Request a new one.";

type ConfirmResponder = (body: Record<string, string>) => Response | Promise<Response>;

function stubConfirm(respond: ConfirmResponder) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/auth/password-reset/confirm") return respond(JSON.parse(String(init?.body)));
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function confirmCalls(fetchMock: ReturnType<typeof stubConfirm>) {
  return fetchMock.mock.calls.filter(([url]) => url === "/auth/password-reset/confirm");
}

function errorResponse(status: number, code: string, message: string) {
  return new Response(JSON.stringify({ error: { code, message } }), { status });
}

async function renderAt(url: string) {
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = createRouter({ routeTree, context: { auth: SIGNED_OUT }, history });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={SIGNED_OUT}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return { router };
}

async function fillPasswords(next: string, confirm = next) {
  fireEvent.change(await screen.findByLabelText("New password"), { target: { value: next } });
  fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: confirm } });
}

function submit() {
  fireEvent.click(screen.getByRole("button", { name: "Set new password" }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("/auth/reset-password", () => {
  it("shows the link-expired card immediately when the token is missing", async () => {
    const fetchMock = stubConfirm(() => new Response("{}"));
    await renderAt("/auth/reset-password?tenant=acme");

    expect(await screen.findByText(EXPIRED_TEXT)).toBeTruthy();
    expect(screen.queryByLabelText("New password")).toBeNull();
    expect(screen.getByRole("link", { name: "Request a new link" }).getAttribute("href")).toBe("/auth/forgot-password");
    expect(confirmCalls(fetchMock)).toHaveLength(0);
  });

  it("refuses a password below strength score 2 without calling the endpoint", async () => {
    const fetchMock = stubConfirm(() => new Response("{}"));
    await renderAt("/auth/reset-password?token=t0k&tenant=acme");

    await fillPasswords(WEAK);
    submit();

    expect(await screen.findByText(/Choose a stronger password/)).toBeTruthy();
    expect(confirmCalls(fetchMock)).toHaveLength(0);
  });

  it("refuses a strong password under 12 characters without calling the endpoint", async () => {
    const fetchMock = stubConfirm(() => new Response("{}"));
    await renderAt("/auth/reset-password?token=t0k&tenant=acme");

    await fillPasswords("Qz#8vL!m2");
    submit();

    expect(await screen.findByText(/Choose a stronger password/)).toBeTruthy();
    expect(confirmCalls(fetchMock)).toHaveLength(0);
  });

  it("flags a mismatch when the confirm field loses focus, not while typing", async () => {
    stubConfirm(() => new Response("{}"));
    await renderAt("/auth/reset-password?token=t0k&tenant=acme");

    await fillPasswords(STRONG, `${STRONG}x`);
    expect(screen.queryByText("Passwords don't match.")).toBeNull();

    fireEvent.blur(screen.getByLabelText("Confirm new password"));
    expect(await screen.findByText("Passwords don't match.")).toBeTruthy();
  });

  it("sends the link's token and tenant, and replaces the form with the link-expired card on a 404", async () => {
    const fetchMock = stubConfirm(() => errorResponse(404, "invalid_token", "reset link is invalid or has expired"));
    await renderAt("/auth/reset-password?token=t0k&tenant=acme");

    await fillPasswords(STRONG);
    submit();

    const heading = await screen.findByRole("heading", { name: "Link expired" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(screen.queryByLabelText("New password")).toBeNull();
    expect(JSON.parse(String(confirmCalls(fetchMock)[0]?.[1]?.body))).toEqual({
      token: "t0k",
      new_password: STRONG,
      tenant: "acme",
    });
  });

  it("shows the server's policy message under the new password on a 422", async () => {
    stubConfirm(() => errorResponse(422, "auth.password_too_weak", "password is too common"));
    await renderAt("/auth/reset-password?token=t0k&tenant=acme");

    await fillPasswords(STRONG);
    submit();

    expect(await screen.findByText("Password is too common.")).toBeTruthy();
    expect((screen.getByLabelText("New password") as HTMLInputElement).value).toBe(STRONG);
  });
});

describe("ResetPasswordPage", () => {
  async function renderPage(outcome: "signed_in" | "login_required") {
    const redirect = vi.fn();
    const confirmReset = vi.fn(async () => outcome);
    const rootRoute = createRootRoute({});
    const page = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: () => <ResetPasswordPage token="t0k" tenant="acme" confirmReset={confirmReset} redirect={redirect} />,
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([page]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);
    return { redirect, confirmReset };
  }

  it("reloads into the app when the response set a session", async () => {
    const { redirect, confirmReset } = await renderPage("signed_in");

    await fillPasswords(STRONG);
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/"));
    expect(confirmReset).toHaveBeenCalledWith({ token: "t0k", newPassword: STRONG, tenant: "acme" });
  });

  it("reloads into sign in when no session was issued", async () => {
    const { redirect } = await renderPage("login_required");

    await fillPasswords(STRONG);
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
  });
});
