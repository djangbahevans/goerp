import {
  AuthContext,
  type AuthContextValue,
  createPermissionContextValue,
  PermissionContext,
  permissionDataRef,
  type RegisterOutcome,
  type Registration,
  type TenantContext,
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
import { RegisterPage, type RegisterPageProps } from "../../auth/register-page.js";
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
const ENABLED: TenantContext = { tenant: null, registrationEnabled: true, termsUrl: null };

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function renderPage(props: RegisterPageProps = {}, tenantContext: TenantContext | null = ENABLED) {
  const redirect = vi.fn();
  const register = vi.fn<(input: Registration) => Promise<RegisterOutcome>>(async () => ({
    kind: "signed_in",
    tenantSlug: "acme-corp",
  }));
  const checkSlug = vi.fn(async () => true);
  const resend = vi.fn(async () => {});
  const queryClient = new QueryClient();
  queryClient.setQueryData(["auth", "tenant-context"], tenantContext);

  const rootRoute = createRootRoute({});
  const page = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <RegisterPage register={register} checkSlug={checkSlug} resend={resend} redirect={redirect} {...props} />
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { redirect, register, checkSlug, resend };
}

async function fillForm({ company = "Acme Corp" }: { company?: string } = {}) {
  fireEvent.change(await screen.findByLabelText("Full name"), { target: { value: "Kwame Mensah" } });
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: " kwame@acme.test " } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: STRONG } });
  fireEvent.change(screen.getByLabelText("Confirm password"), { target: { value: STRONG } });
  fireEvent.change(screen.getByLabelText("Company name"), { target: { value: company } });
}

function submit() {
  fireEvent.click(screen.getByRole("button", { name: "Create account" }));
}

describe("/auth/register", () => {
  it("renders the form when the platform allows registration", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/auth/tenant-context") {
          return new Response(JSON.stringify({ tenant: null, registration_enabled: true, terms_url: null }));
        }
        return new Response(JSON.stringify({ available: true }));
      }),
    );
    const history = createMemoryHistory({ initialEntries: ["/auth/register"] });
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

    expect(await screen.findByRole("heading", { name: "Create your account" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Sign in" }).getAttribute("href")).toBe("/auth/login");
  });
});

describe("RegisterPage", () => {
  it("redirects to sign in without rendering the form when registration is off", async () => {
    const { redirect } = await renderPage({}, { tenant: null, registrationEnabled: false, termsUrl: null });

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
    expect(screen.queryByLabelText("Full name")).toBeNull();
  });

  it("redirects to sign in when the tenant context can't be loaded", async () => {
    const { redirect } = await renderPage({}, null);

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
  });

  it("submits the trimmed form and reloads into the app on a signed-in response", async () => {
    const { register, redirect } = await renderPage();

    await fillForm();
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/"));
    expect(register).toHaveBeenCalledWith({
      name: "Kwame Mensah",
      email: "kwame@acme.test",
      password: STRONG,
      companyName: "Acme Corp",
    });
  });

  it("reloads into sign in when the account exists but no session was issued", async () => {
    const { redirect } = await renderPage({
      register: async () => ({ kind: "login_required", tenantSlug: "acme-corp" }),
    });

    await fillForm();
    submit();

    await waitFor(() => expect(redirect).toHaveBeenCalledWith("/auth/login"));
  });

  it("validates empty fields without calling the API", async () => {
    const { register } = await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "Create account" }));

    expect(await screen.findByText("Enter your name.")).toBeTruthy();
    expect(screen.getByText("Enter your email address.")).toBeTruthy();
    expect(screen.getByText("Enter a password.")).toBeTruthy();
    expect(screen.getByText("Confirm your password.")).toBeTruthy();
    expect(screen.getByText("Enter your company name.")).toBeTruthy();
    expect(register).not.toHaveBeenCalled();
  });

  it("refuses a company name that yields no valid slug", async () => {
    const { register } = await renderPage();

    await fillForm({ company: "AB" });
    submit();

    expect(await screen.findByText("Use at least 3 letters or digits in your company name.")).toBeTruthy();
    expect(register).not.toHaveBeenCalled();
  });

  it("checks the derived slug after a 500ms pause and shows it's available", async () => {
    const { checkSlug } = await renderPage();

    fireEvent.change(await screen.findByLabelText("Company name"), { target: { value: "Café Ghana Ltd." } });
    expect(checkSlug).not.toHaveBeenCalled();

    expect(await screen.findByText("Available")).toBeTruthy();
    expect(checkSlug).toHaveBeenCalledTimes(1);
    expect(checkSlug).toHaveBeenCalledWith("cafe-ghana-ltd", expect.any(AbortSignal));
  });

  it("clears the previous name's availability as soon as the name changes", async () => {
    await renderPage({ checkSlug: async (slug: string) => slug === "acme" });

    const company = await screen.findByLabelText("Company name");
    fireEvent.change(company, { target: { value: "Acme" } });
    expect(await screen.findByText("Available")).toBeTruthy();

    fireEvent.change(company, { target: { value: "Acme Corp" } });
    expect(screen.queryByText("Available")).toBeNull();
    expect(await screen.findByText("Name taken")).toBeTruthy();
  });

  it("says the server is busy on a 503 overloaded", async () => {
    await renderPage({
      register: async () => {
        throw new AppError({ code: "overloaded", message: "", httpStatus: 503, details: { retryAfter: 1 } });
      },
    });

    await fillForm();
    submit();

    expect(await screen.findByText("The server is busy. Try again in a moment.")).toBeTruthy();
  });

  it("shows a taken slug inline", async () => {
    await renderPage({ checkSlug: async () => false });

    fireEvent.change(await screen.findByLabelText("Company name"), { target: { value: "Acme Corp" } });

    expect(await screen.findByText("Name taken")).toBeTruthy();
  });

  it("shows the terms checkbox only when a terms URL is configured, and requires it", async () => {
    const { register } = await renderPage({}, { ...ENABLED, termsUrl: "https://example.com/terms" });

    const link = await screen.findByRole("link", { name: /^terms of service/ });
    expect(link.getAttribute("href")).toBe("https://example.com/terms");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.textContent).toContain("(opens in a new tab)");
    await fillForm();
    submit();
    expect(await screen.findByText("Accept the terms of service to continue.")).toBeTruthy();
    expect(register).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("checkbox"));
    submit();
    await waitFor(() => expect(register).toHaveBeenCalledTimes(1));
  });

  it("has no terms checkbox when no terms URL is configured", async () => {
    await renderPage();

    await screen.findByLabelText("Full name");
    expect(screen.queryByRole("checkbox")).toBeNull();
  });

  it("puts a 409 on the field that caused it", async () => {
    const conflict = (code: string) => async () => {
      throw new AppError({ code, message: "", httpStatus: 409 });
    };

    await renderPage({ register: conflict("auth.email_already_exists") });
    await fillForm();
    submit();
    expect(await screen.findByText("Email already in use")).toBeTruthy();
    cleanup();

    await renderPage({ register: conflict("tenant.slug_taken") });
    await fillForm();
    submit();
    expect(await screen.findByText("Company name taken, try another")).toBeTruthy();
  });

  it("shows a 422's messages under each named field", async () => {
    await renderPage({
      register: async () => {
        throw new AppError({
          code: "validation_failed",
          message: "some fields are invalid",
          httpStatus: 422,
          details: { email: "Enter a valid email address.", password: "password is too common" },
        });
      },
    });

    await fillForm();
    submit();

    expect(await screen.findByText("Enter a valid email address.")).toBeTruthy();
    expect(screen.getByText("Password is too common.")).toBeTruthy();
  });

  it("replaces the form with the check-your-email card and resends to the response's tenant", async () => {
    const { resend } = await renderPage({
      register: async () => ({ kind: "verification_required", tenantSlug: "acme-corp" }),
    });

    await fillForm();
    submit();

    const heading = await screen.findByRole("heading", { name: "Check your email" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(screen.getByText("kwame@acme.test")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Back to sign in" }).getAttribute("href")).toBe("/auth/login");

    const resendButton = screen.getByRole("button", { name: "Resend email" }) as HTMLButtonElement;
    fireEvent.click(resendButton);

    expect(
      await screen.findByText(/Sent\. Check your inbox and spam folder\. Resend again in 60 seconds\./),
    ).toBeTruthy();
    expect(resendButton.disabled).toBe(true);
    expect(resend).toHaveBeenCalledWith({ email: "kwame@acme.test", tenant: "acme-corp" });
  });

  it("tells the user to sign in shortly while the workspace is still provisioning", async () => {
    await renderPage({ register: async () => ({ kind: "provisioning_pending", tenantSlug: "acme-corp" }) });

    await fillForm();
    submit();

    const heading = await screen.findByRole("heading", { name: "Your workspace is almost ready" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(screen.getByText("Sign in in a minute.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Sign in" }).getAttribute("href")).toBe("/auth/login");
  });

  it("keeps the form and shows a retryable error on a network failure", async () => {
    await renderPage({
      register: async () => {
        throw new TypeError("Failed to fetch");
      },
    });

    await fillForm();
    submit();

    expect(await screen.findByText("Couldn't reach the server. Check your connection and try again.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Create account" }) as HTMLButtonElement).disabled).toBe(false);
  });
});
