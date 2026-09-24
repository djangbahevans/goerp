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
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { VerifyEmailPage } from "../../auth/verify-email-page.js";
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

const SENT_TEXT = "If your account still needs verifying, a new link is on its way.";

type Responder = (body: Record<string, string>) => Response | Promise<Response>;

function stubAPI({ verify, resend }: { verify?: Responder; resend?: Responder }) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body ?? "{}"));
    if (url === "/auth/verify-email" && verify) return verify(body);
    if (url === "/auth/verify-email/resend" && resend) return resend(body);
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function callsTo(fetchMock: ReturnType<typeof stubAPI>, url: string) {
  return fetchMock.mock.calls.filter(([u]) => u === url);
}

function errorResponse(status: number, code: string, message: string, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify({ error: { code, message } }), { status, headers });
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

async function clickVerify() {
  fireEvent.click(await screen.findByRole("button", { name: "Verify email" }));
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("/auth/verify-email", () => {
  it("waits for a click before verifying, then sends the link's token and tenant", async () => {
    const fetchMock = stubAPI({ verify: () => new Response(JSON.stringify({ login_required: true })) });
    await renderAt("/auth/verify-email?token=t0k&tenant=acme");

    expect(await screen.findByRole("heading", { name: "Verify your email" })).toBeTruthy();
    expect(callsTo(fetchMock, "/auth/verify-email")).toHaveLength(0);

    await clickVerify();

    await waitFor(() => expect(callsTo(fetchMock, "/auth/verify-email")).toHaveLength(1));
    expect(JSON.parse(String(callsTo(fetchMock, "/auth/verify-email")[0]?.[1]?.body))).toEqual({
      token: "t0k",
      tenant: "acme",
    });
  });

  it("verifies a link with no tenant, sending an empty tenant", async () => {
    const fetchMock = stubAPI({ verify: () => new Response(JSON.stringify({ login_required: true })) });
    await renderAt("/auth/verify-email?token=t0k");

    await clickVerify();

    await waitFor(() => expect(callsTo(fetchMock, "/auth/verify-email")).toHaveLength(1));
    expect(JSON.parse(String(callsTo(fetchMock, "/auth/verify-email")[0]?.[1]?.body))).toEqual({
      token: "t0k",
      tenant: "",
    });
  });

  it("shows the expired card without calling the API when the token is missing", async () => {
    const fetchMock = stubAPI({ verify: () => new Response("{}") });
    await renderAt("/auth/verify-email?tenant=acme");

    expect(await screen.findByRole("heading", { name: "This link has expired" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Verify email" })).toBeNull();
    expect(screen.getByLabelText("Email")).toBeTruthy();
    expect(callsTo(fetchMock, "/auth/verify-email")).toHaveLength(0);
  });

  it("offers only sign in on the expired card when the URL has no tenant", async () => {
    stubAPI({});
    await renderAt("/auth/verify-email");

    expect(await screen.findByRole("heading", { name: "This link has expired" })).toBeTruthy();
    expect(screen.queryByLabelText("Email")).toBeNull();
    expect(screen.queryByRole("button", { name: "Send a new link" })).toBeNull();
    expect(screen.getByRole("link", { name: "Sign in" }).getAttribute("href")).toBe("/auth/login");
  });

  it("replaces the page with the expired card on a 404 and focuses its heading", async () => {
    stubAPI({ verify: () => errorResponse(404, "invalid_token", "verification link is invalid or has expired") });
    await renderAt("/auth/verify-email?token=t0k&tenant=acme");

    await clickVerify();

    const heading = await screen.findByRole("heading", { name: "This link has expired" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
  });

  it("resends from the expired card and holds the button for 60 seconds", async () => {
    const fetchMock = stubAPI({ resend: () => new Response(JSON.stringify({ status: "ok" })) });
    await renderAt("/auth/verify-email?tenant=acme");

    fireEvent.change(await screen.findByLabelText("Email"), { target: { value: " ada@example.com " } });
    vi.useFakeTimers({ shouldAdvanceTime: false, toFake: ["setInterval", "clearInterval", "Date"] });
    const send = screen.getByRole("button", { name: "Send a new link" }) as HTMLButtonElement;
    await act(async () => {
      fireEvent.click(send);
    });

    const status = await screen.findByText(new RegExp(SENT_TEXT));
    expect(status.getAttribute("role")).toBe("status");
    expect(status.textContent).toBe(`${SENT_TEXT} Resend again in 60 seconds.`);
    expect(send.disabled).toBe(true);
    expect(JSON.parse(String(callsTo(fetchMock, "/auth/verify-email/resend")[0]?.[1]?.body))).toEqual({
      email: "ada@example.com",
      tenant: "acme",
    });

    await act(async () => {
      vi.advanceTimersByTime(60_200);
    });
    expect(status.textContent).toBe(SENT_TEXT);
    expect(send.disabled).toBe(false);
  });

  it("on a rate-limited resend shows a countdown instead of the confirmation and holds the button", async () => {
    stubAPI({
      resend: () => errorResponse(429, "rate_limit_exceeded", "too many requests", { "Retry-After": "9" }),
    });
    await renderAt("/auth/verify-email?tenant=acme");

    fireEvent.change(await screen.findByLabelText("Email"), { target: { value: "ada@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Send a new link" }));

    expect(await screen.findByText("Too many attempts. Try again in 9 seconds.")).toBeTruthy();
    expect(screen.queryByText(new RegExp(SENT_TEXT))).toBeNull();
    expect((screen.getByRole("button", { name: "Send a new link" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("asks for an email before resending", async () => {
    const fetchMock = stubAPI({ resend: () => new Response("{}") });
    await renderAt("/auth/verify-email?tenant=acme");

    fireEvent.click(await screen.findByRole("button", { name: "Send a new link" }));

    expect(await screen.findByText("Enter your email address.")).toBeTruthy();
    expect(callsTo(fetchMock, "/auth/verify-email/resend")).toHaveLength(0);
  });

  it("on 429 shows a countdown from Retry-After and keeps the button disabled", async () => {
    stubAPI({
      verify: () => errorResponse(429, "rate_limit_exceeded", "too many requests", { "Retry-After": "7" }),
    });
    await renderAt("/auth/verify-email?token=t0k&tenant=acme");

    await clickVerify();

    expect(await screen.findByText("Too many attempts. Try again in 7 seconds.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Verify email" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows a retryable error on an unexpected response", async () => {
    stubAPI({ verify: () => errorResponse(500, "internal_error", "email verification failed") });
    await renderAt("/auth/verify-email?token=t0k&tenant=acme");

    await clickVerify();

    expect((await screen.findByRole("alert")).textContent).toBe("Couldn't verify your email. Try again.");
    expect((screen.getByRole("button", { name: "Verify email" }) as HTMLButtonElement).disabled).toBe(false);
  });
});

describe("VerifyEmailPage", () => {
  async function renderPage(outcome: "signed_in" | "login_required") {
    const redirect = vi.fn();
    const verify = vi.fn(async () => outcome);
    const rootRoute = createRootRoute({});
    const page = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: () => <VerifyEmailPage token="t0k" tenant="acme" verify={verify} redirect={redirect} />,
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([page]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);
    return { redirect, verify };
  }

  it("reloads into the app when the response set a session", async () => {
    const { redirect, verify } = await renderPage("signed_in");

    await clickVerify();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/"));
    expect(verify).toHaveBeenCalledWith({ token: "t0k", tenant: "acme" });
  });

  it("reloads into sign in with the email_verified notice when no session was issued", async () => {
    const { redirect } = await renderPage("login_required");

    await clickVerify();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login?notice=email_verified"));
  });
});
