import type { AuthContextValue, AuthState, MFAMethod } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { normalizeRecoveryCode } from "../../auth/mfa-challenge-page.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const FAKE_USER = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd", "totp"],
  mfaVerifiedAt: null,
};
const FAKE_TENANT = { id: "t1", slug: "acme", name: "Acme Corp", plan: "pro" };

type SubmitImpl = (code: string, method: MFAMethod, setState: (state: AuthState) => void) => Promise<void>;

function challenge(methods: MFAMethod[] = ["totp", "recovery_code"]): AuthState {
  return { status: "mfa_required", challengeToken: "tok", methods };
}

// Stateful stand-in for AuthProvider, mirroring the real submitMFA's
// transitions: success → authenticated, server rejection → unauthenticated.
function FakeAuthProvider({
  initial,
  submitImpl,
  children,
}: {
  initial: AuthState;
  submitImpl: SubmitImpl;
  children: ReactNode;
}) {
  const [state, setState] = useState<AuthState>(initial);
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  const value: AuthContextValue = {
    state,
    isAuthenticated,
    user: isAuthenticated ? state.user : null,
    tenant: isAuthenticated ? state.tenant : null,
    login: async () => {},
    logout: async () => {},
    submitMFA: (code, method = "totp") => submitImpl(code, method, setState),
    updateProfile: async () => {},
    changePassword: async () => {},
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

const succeed: SubmitImpl = async (_code, _method, setState) =>
  setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT });

function rejectWith(err: AppError): SubmitImpl {
  return async (_code, _method, setState) => {
    setState({ status: "unauthenticated" });
    throw err;
  };
}

async function renderMFA({
  url = "/auth/mfa?redirect=%2Fsettings%2Fprofile",
  initial = challenge(),
  submitImpl = vi.fn<SubmitImpl>(succeed),
}: {
  url?: string;
  initial?: AuthState;
  submitImpl?: SubmitImpl;
} = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ tenant: null, registration_enabled: false }), { status: 200 })),
  );
  const router = createRouter({
    routeTree,
    context: { auth: undefined! },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider initial={initial} submitImpl={submitImpl}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  return { router, submitImpl };
}

function codeInput(): HTMLInputElement {
  return screen.getByLabelText("Verification code") as HTMLInputElement;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("normalizeRecoveryCode", () => {
  it.each([
    ["ABCDE-FGHIJ", "ABCDE-FGHIJ"],
    ["abcde-fghij", "ABCDE-FGHIJ"],
    [" abcde fghij ", "ABCDE-FGHIJ"],
    ["ABCDEFGH23", "ABCDE-FGH23"],
  ])("normalizes %j to %j", (raw, want) => {
    expect(normalizeRecoveryCode(raw)).toBe(want);
  });

  it.each(["ABCDE-FGHI", "ABCDE-FGHIJK", "ABCDE-FGH18", ""])("rejects %j", (raw) => {
    expect(normalizeRecoveryCode(raw)).toBeNull();
  });
});

describe("/auth/mfa", () => {
  it("auto-submits six typed digits and redirects to ?redirect on success", async () => {
    const { router, submitImpl } = await renderMFA();

    expect(await screen.findByRole("heading", { name: "Enter your verification code" })).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(codeInput()));
    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(submitImpl).toHaveBeenCalledWith("123456", "totp", expect.any(Function));
  });

  it("auto-submits a pasted code with a separator", async () => {
    const { submitImpl } = await renderMFA();
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123 456" } });

    await waitFor(() => expect(submitImpl).toHaveBeenCalledWith("123456", "totp", expect.any(Function)));
  });

  it("redirects to / when no redirect param is given", async () => {
    const { router } = await renderMFA({ url: "/auth/mfa" });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("verifies a normalized recovery code", async () => {
    const { router, submitImpl } = await renderMFA();
    fireEvent.click(await screen.findByRole("button", { name: "Use a recovery code instead" }));

    const recovery = screen.getByLabelText("Recovery code");
    await waitFor(() => expect(document.activeElement).toBe(recovery));
    fireEvent.change(recovery, { target: { value: "abcde fghij" } });
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(submitImpl).toHaveBeenCalledWith("ABCDE-FGHIJ", "recovery_code", expect.any(Function));
  });

  it("rejects a malformed recovery code locally without spending the challenge", async () => {
    const { submitImpl } = await renderMFA();
    fireEvent.click(await screen.findByRole("button", { name: "Use a recovery code instead" }));

    fireEvent.change(screen.getByLabelText("Recovery code"), { target: { value: "ABCDE" } });
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));

    expect(await screen.findByText("Enter a recovery code in the form XXXXX-XXXXX.")).toBeTruthy();
    expect(submitImpl).not.toHaveBeenCalled();
  });

  it("asks for all six digits when Verify is pressed early", async () => {
    const { submitImpl } = await renderMFA();
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123" } });
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));

    expect(await screen.findByText("Enter the 6-digit code.")).toBeTruthy();
    expect(submitImpl).not.toHaveBeenCalled();
  });

  it("sends a rejected code back to sign in with an explanation", async () => {
    const { router } = await renderMFA({
      submitImpl: rejectWith(new AppError({ code: "invalid_mfa_code", message: "invalid MFA code", httpStatus: 401 })),
    });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile", notice: "mfa_failed" });
    expect(await screen.findByText("Incorrect or expired code. Sign in again.")).toBeTruthy();
  });

  it("sends a locked-out user back to sign in with the lockout message", async () => {
    const { router } = await renderMFA({
      submitImpl: rejectWith(new AppError({ code: "mfa_locked", message: "locked", httpStatus: 423 })),
    });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.search).toMatchObject({ notice: "mfa_locked" }));
    expect(await screen.findByText("Too many failed verification attempts. Try again later.")).toBeTruthy();
  });

  it("doesn't blame the code when verification succeeds but the session check fails", async () => {
    const { router } = await renderMFA({
      submitImpl: async (_code, _method, setState) => {
        setState({ status: "unauthenticated" });
        throw new Error("mfa verification succeeded but the session check that follows it failed");
      },
    });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.search).toMatchObject({ notice: "session_failed" }));
    expect(await screen.findByText("Couldn't finish signing you in. Sign in again.")).toBeTruthy();
  });

  it("stays on the page with a retry message after a network failure", async () => {
    const submitImpl = vi
      .fn<SubmitImpl>()
      .mockImplementationOnce(async () => {
        throw new TypeError("Failed to fetch");
      })
      .mockImplementation(succeed);
    const { router } = await renderMFA({ submitImpl });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "123456" } });

    expect(await screen.findByText("Couldn't verify the code. Check your connection and try again.")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/auth/mfa");
    expect(codeInput().value).toBe("");
    await waitFor(() => expect(document.activeElement).toBe(codeInput()));

    fireEvent.change(codeInput(), { target: { value: "654321" } });
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
  });

  it("sends a visit with no pending challenge to sign in, keeping the redirect", async () => {
    const { router } = await renderMFA({ initial: { status: "unauthenticated" } });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("offers only the recovery code when TOTP isn't enrolled", async () => {
    await renderMFA({ initial: challenge(["recovery_code"]) });

    const recovery = await screen.findByLabelText("Recovery code");
    expect(screen.queryByRole("button", { name: /instead/ })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(recovery));
  });

  it("explains when no enrolled method can be used here", async () => {
    await renderMFA({ initial: challenge(["webauthn"]) });

    expect(await screen.findByText(/can't be used on this page yet/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Back to sign in" }).getAttribute("href")).toBe(
      "/auth/login?redirect=%2Fsettings%2Fprofile",
    );
  });
});
