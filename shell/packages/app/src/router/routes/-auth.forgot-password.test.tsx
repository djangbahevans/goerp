import {
  AuthContext,
  type AuthContextValue,
  createPermissionContextValue,
  PermissionContext,
  permissionDataRef,
} from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
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
};

const SUBDOMAIN_TENANT = { tenant: { slug: "acme", name: "Acme Corp" }, registration_enabled: false };
const SHARED_DOMAIN = { tenant: null, registration_enabled: false };
const SUCCESS_MESSAGE = "If that email is registered, you'll receive a reset link shortly.";

type ResetResponder = (body: { email: string; tenant: string }) => Response | Promise<Response>;

function stubFetch(tenantContext: unknown, onReset: ResetResponder = () => new Response('{"status":"ok"}')) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/auth/tenant-context") return new Response(JSON.stringify(tenantContext), { status: 200 });
    if (url === "/auth/password-reset/request") return onReset(JSON.parse(String(init?.body)));
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function resetCalls(fetchMock: ReturnType<typeof stubFetch>) {
  return fetchMock.mock.calls.filter(([url]) => url === "/auth/password-reset/request");
}

async function renderPage() {
  const history = createMemoryHistory({ initialEntries: ["/auth/forgot-password"] });
  const router = createRouter({ routeTree, context: { auth: SIGNED_OUT }, history });
  await router.load();
  const result = render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={SIGNED_OUT}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  const submit = await screen.findByRole("button", { name: "Send reset link" });
  await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
  return { ...result, submit };
}

function submitEmail(submit: HTMLElement, email: string) {
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: email } });
  fireEvent.click(submit);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("/auth/forgot-password", () => {
  it("sends the email with the subdomain-resolved tenant, then shows the generic success state", async () => {
    const fetchMock = stubFetch(SUBDOMAIN_TENANT);
    const { submit } = await renderPage();
    expect(screen.queryByLabelText("Company")).toBeNull();

    submitEmail(submit, "  ada@example.com ");

    const heading = await screen.findByRole("heading", { name: "Check your email" });
    expect(screen.getByText(SUCCESS_MESSAGE)).toBeTruthy();
    expect(document.activeElement).toBe(heading);
    expect(screen.getByRole("link", { name: "Back to sign in" }).getAttribute("href")).toBe("/auth/login");
    expect(resetCalls(fetchMock)).toHaveLength(1);
    expect(JSON.parse(String(resetCalls(fetchMock)[0]?.[1]?.body))).toEqual({
      email: "ada@example.com",
      tenant: "acme",
    });
  });

  it("renders an identical success state for a registered and an unregistered email", async () => {
    stubFetch(SUBDOMAIN_TENANT);
    const registered = await renderPage();
    submitEmail(registered.submit, "ada@example.com");
    await screen.findByText(SUCCESS_MESSAGE);
    const registeredHtml = registered.container.innerHTML;
    cleanup();

    stubFetch(SUBDOMAIN_TENANT);
    const unregistered = await renderPage();
    submitEmail(unregistered.submit, "nobody@example.com");
    await screen.findByText(SUCCESS_MESSAGE);

    expect(unregistered.container.innerHTML).toBe(registeredHtml);
  });

  it("asks for the company on a shared domain and sends it as the tenant", async () => {
    const fetchMock = stubFetch(SHARED_DOMAIN);
    const { submit } = await renderPage();

    submitEmail(submit, "ada@example.com");
    expect(await screen.findByText("Enter your company.")).toBeTruthy();
    expect(resetCalls(fetchMock)).toHaveLength(0);

    fireEvent.change(screen.getByLabelText("Company"), { target: { value: " globex " } });
    fireEvent.click(submit);

    await screen.findByText(SUCCESS_MESSAGE);
    expect(JSON.parse(String(resetCalls(fetchMock)[0]?.[1]?.body))).toEqual({
      email: "ada@example.com",
      tenant: "globex",
    });
  });

  it("requires an email without calling the endpoint", async () => {
    const fetchMock = stubFetch(SUBDOMAIN_TENANT);
    const { submit } = await renderPage();

    fireEvent.click(submit);

    expect(await screen.findByText("Enter your email address.")).toBeTruthy();
    expect(resetCalls(fetchMock)).toHaveLength(0);
  });

  it("shows a countdown and keeps the form on a 429", async () => {
    stubFetch(
      SUBDOMAIN_TENANT,
      () =>
        new Response(JSON.stringify({ error: { code: "rate_limited", message: "slow down" } }), {
          status: 429,
          headers: { "Retry-After": "30" },
        }),
    );
    const { submit } = await renderPage();

    submitEmail(submit, "ada@example.com");

    expect(await screen.findByText(/Too many requests\. Try again in/)).toBeTruthy();
    expect(screen.queryByText(SUCCESS_MESSAGE)).toBeNull();
    expect((submit as HTMLButtonElement).disabled).toBe(true);
  });

  it("keeps the form with a connection error when the request fails outright", async () => {
    stubFetch(SUBDOMAIN_TENANT, () => {
      throw new TypeError("network down");
    });
    const { submit } = await renderPage();

    submitEmail(submit, "ada@example.com");

    expect(await screen.findByText("Couldn't reach the server. Check your connection and try again.")).toBeTruthy();
    expect(screen.queryByText(SUCCESS_MESSAGE)).toBeNull();
    expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("ada@example.com");
  });
});
