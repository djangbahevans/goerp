import {
  AuthContext,
  type AuthContextValue,
  createPermissionContextValue,
  PermissionContext,
  permissionDataRef,
} from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
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
import { AcceptInvitePage } from "../../auth/accept-invite-page.js";
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
  changePassword: async () => {},
  reloadSession: async () => {},
};

const STRONG = "Plinth-Quartz-Meadow-47";
const WEAK = "password1234";
const LINK = "/auth/accept-invite?token=t0k&tenant=acme";
const NEW_USER_INFO = { tenant_name: "Acme Corp", email: "kwame@acme.com", name: null, password_required: true };
const EXISTING_USER_INFO = { ...NEW_USER_INFO, password_required: false };

type Responder = (body: Record<string, string>) => Response | Promise<Response>;

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status });
}

function stubInvite(info: Response | (() => Response), accept: Responder = () => json(200, { expires_in: 900 })) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.startsWith("/auth/accept-invite/info?")) return typeof info === "function" ? info() : info.clone();
    if (url === "/auth/accept-invite") return accept(JSON.parse(String(init?.body)));
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function calls(fetchMock: ReturnType<typeof stubInvite>, prefix: string) {
  return fetchMock.mock.calls.filter(([url]) => url.startsWith(prefix));
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
}

async function fillPasswords(next: string, confirm = next) {
  fireEvent.change(await screen.findByLabelText("New password"), { target: { value: next } });
  fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: confirm } });
}

function submit() {
  fireEvent.click(screen.getByRole("button", { name: "Set password and sign in" }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("/auth/accept-invite", () => {
  it("shows the expired card without calling the server when the link is incomplete", async () => {
    const fetchMock = stubInvite(json(200, NEW_USER_INFO));
    await renderAt("/auth/accept-invite?token=t0k");

    expect(await screen.findByRole("heading", { name: "Invite expired" })).toBeTruthy();
    expect(calls(fetchMock, "/auth/accept-invite")).toHaveLength(0);
  });

  it("shows the expired card when the info lookup 404s", async () => {
    stubInvite(json(404, { error: { code: "invalid_invite", message: "invite link is invalid or has expired" } }));
    await renderAt(LINK);

    const heading = await screen.findByRole("heading", { name: "Invite expired" });
    expect(screen.queryByLabelText("New password")).toBeNull();
    expect(document.activeElement).not.toBe(heading);
  });

  it("welcomes a new invitee with the tenant name and a prefilled, read-only email", async () => {
    const fetchMock = stubInvite(json(200, NEW_USER_INFO));
    await renderAt(LINK);

    expect(await screen.findByRole("heading", { name: "You've been invited to Acme Corp" })).toBeTruthy();
    const email = screen.getByLabelText("Email") as HTMLInputElement;
    expect(email.value).toBe("kwame@acme.com");
    expect(email.readOnly).toBe(true);
    expect(calls(fetchMock, "/auth/accept-invite/info")[0]?.[0]).toBe("/auth/accept-invite/info?token=t0k&tenant=acme");
  });

  it("refuses a weak password without calling accept", async () => {
    const fetchMock = stubInvite(json(200, NEW_USER_INFO));
    await renderAt(LINK);

    await fillPasswords(WEAK);
    submit();

    expect(await screen.findByText(/Choose a stronger password/)).toBeTruthy();
    expect(fetchMock.mock.calls.filter(([url]) => url === "/auth/accept-invite")).toHaveLength(0);
  });

  it("sends token, tenant, and password, and shows the expired card if the invite died meanwhile", async () => {
    const fetchMock = stubInvite(json(200, NEW_USER_INFO), () =>
      json(404, { error: { code: "invalid_invite", message: "invite link is invalid or has expired" } }),
    );
    await renderAt(LINK);

    await fillPasswords(STRONG);
    submit();

    const heading = await screen.findByRole("heading", { name: "Invite expired" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    const acceptCall = fetchMock.mock.calls.find(([url]) => url === "/auth/accept-invite");
    expect(JSON.parse(String(acceptCall?.[1]?.body))).toEqual({ token: "t0k", tenant: "acme", password: STRONG });
  });

  it("shows the server's policy message on a 422", async () => {
    stubInvite(json(200, NEW_USER_INFO), () =>
      json(422, { error: { code: "auth.password_too_weak", message: "password is too common" } }),
    );
    await renderAt(LINK);

    await fillPasswords(STRONG);
    submit();

    expect(await screen.findByText("Password is too common.")).toBeTruthy();
  });

  it("tells the invitee to sign in on a 409 conflict", async () => {
    stubInvite(json(200, NEW_USER_INFO), () =>
      json(409, { error: { code: "invite_conflict", message: "this account was set up by another request" } }),
    );
    await renderAt(LINK);

    await fillPasswords(STRONG);
    submit();

    expect(await screen.findByText("This account has already been set up. Sign in instead.")).toBeTruthy();
  });

  it("shows an existing account the access message and no password form, accepting only on request", async () => {
    const fetchMock = stubInvite(json(200, EXISTING_USER_INFO), () => json(200, { login_required: true }));
    await renderAt(LINK);

    expect(
      await screen.findByText(
        "Accept to add Acme Corp to your existing account (kwame@acme.com), then sign in with your existing password.",
      ),
    ).toBeTruthy();
    expect(screen.queryByLabelText("New password")).toBeNull();
    expect(fetchMock.mock.calls.filter(([url]) => url === "/auth/accept-invite")).toHaveLength(0);
  });

  it("shows an existing account's accept failure and lets it retry", async () => {
    let attempts = 0;
    const fetchMock = stubInvite(json(200, EXISTING_USER_INFO), () => {
      attempts += 1;
      return json(500, { error: { code: "internal_error", message: "invite acceptance failed" } });
    });
    await renderAt(LINK);

    fireEvent.click(await screen.findByRole("button", { name: "Accept and sign in" }));

    expect(await screen.findByText("Something went wrong. Try again.")).toBeTruthy();
    const acceptCall = fetchMock.mock.calls.find(([url]) => url === "/auth/accept-invite");
    expect(JSON.parse(String(acceptCall?.[1]?.body))).toEqual({ token: "t0k", tenant: "acme" });

    fireEvent.click(screen.getByRole("button", { name: "Accept and sign in" }));
    await waitFor(() => expect(attempts).toBe(2));
  });
});

describe("AcceptInvitePage", () => {
  async function renderPage(accept: () => Promise<"signed_in" | "login_required">, passwordRequired = true) {
    const redirect = vi.fn();
    const loadInfo = vi.fn(async () => ({
      tenantName: "Acme Corp",
      email: "kwame@acme.com",
      name: null,
      passwordRequired,
    }));
    const rootRoute = createRootRoute({});
    const page = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: () => (
        <AcceptInvitePage token="t0k" tenant="acme" loadInfo={loadInfo} accept={accept} redirect={redirect} />
      ),
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([page]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    return { redirect };
  }

  it("reloads into the app when accepting set a session", async () => {
    const { redirect } = await renderPage(async () => "signed_in");

    await fillPasswords(STRONG);
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/"));
  });

  it("reloads into sign in when no session was issued", async () => {
    const { redirect } = await renderPage(async () => "login_required");

    await fillPasswords(STRONG);
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
  });

  it("sends an existing account to sign in once accepting grants access", async () => {
    const { redirect } = await renderPage(async () => "login_required", false);

    fireEvent.click(await screen.findByRole("button", { name: "Accept and sign in" }));

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
  });

  it("shows the busy message on a 503", async () => {
    await renderPage(async () => {
      throw new AppError({ code: "overloaded", message: "too many password operations", httpStatus: 503 });
    });

    await fillPasswords(STRONG);
    submit();

    expect(await screen.findByText("The server is busy. Try again in a moment.")).toBeTruthy();
  });
});
