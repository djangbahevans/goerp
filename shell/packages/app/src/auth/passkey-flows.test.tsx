import { AuthContext, type AuthContextValue, type MFAFactors } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TwoFactorSection } from "../settings/two-factor-section.js";
import { MFAChallengePage } from "./mfa-challenge-page.js";
import { MFASetupPage } from "./mfa-setup-page.js";
import { PasskeyEnrollmentForm } from "./passkey-enrollment-form.js";

const { supported, begin, confirm, assertion, reverify } = vi.hoisted(() => ({
  supported: vi.fn(),
  begin: vi.fn(),
  confirm: vi.fn(),
  assertion: vi.fn(),
  reverify: vi.fn(),
}));

vi.mock("@goerp/sdk/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@goerp/sdk/auth")>()),
  supportsPasskeys: supported,
  beginPasskeyEnrollment: begin,
  confirmPasskeyEnrollment: confirm,
  requestPasskeyAssertion: assertion,
  reverifyMFA: reverify,
}));

const proof = { type: "webauthn" as const, ceremonyId: "assertion", response: {} as AuthenticationResponseJSON };
const enrollment = { ceremonyId: "registration", response: {} as RegistrationResponseJSON };
const auth: AuthContextValue = {
  state: { status: "mfa_required", challengeToken: "token", methods: ["webauthn"] },
  isAuthenticated: false,
  user: null,
  tenant: null,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: vi.fn(),
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: vi.fn(),
  expireSession: () => {},
};

const tenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  firstDayOfWeek: "monday" as const,
  numberFormat: "1,234.56" as const,
  passwordMinLength: 12,
};

async function mount(children: ReactNode, value: AuthContextValue = auth) {
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  supported.mockReturnValue(true);
  begin.mockResolvedValue(enrollment);
  confirm.mockResolvedValue(null);
  assertion.mockResolvedValue(proof);
  reverify.mockResolvedValue(undefined);
});

afterEach(cleanup);

describe("passkey shell flows", () => {
  it("submits an approved login assertion with the current challenge", async () => {
    await mount(<MFAChallengePage redirectTo="/" />);
    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    await waitFor(() => expect(auth.submitMFA).toHaveBeenCalledWith(proof));
    expect(assertion).toHaveBeenCalledWith({ mfaToken: "token", signal: expect.any(AbortSignal) });
    expect(screen.queryByLabelText("Verification code")).toBeNull();
  });

  it("keeps a cancelled login challenge and returns focus to its passkey button", async () => {
    assertion.mockResolvedValueOnce(null);
    await mount(<MFAChallengePage redirectTo="/" />);
    const button = await screen.findByRole("button", { name: "Use a passkey" });
    fireEvent.click(button);
    await waitFor(() => expect(button).toBe(document.activeElement));
    expect(auth.submitMFA).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.click(button);
    await waitFor(() => expect(auth.submitMFA).toHaveBeenCalledWith(proof));
  });

  it("shows an options error without submitting or discarding the challenge", async () => {
    assertion.mockRejectedValueOnce(new AppError({ code: "mfa_locked", message: "locked", httpStatus: 423 }));
    await mount(<MFAChallengePage redirectTo="/" />);
    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Too many failed verification attempts. Try again later.",
    );
    expect(auth.submitMFA).not.toHaveBeenCalled();
  });

  it("hides passkeys when browser support is missing", async () => {
    supported.mockReturnValue(false);
    await mount(<MFAChallengePage redirectTo="/" />);
    expect(await screen.findByText(/can't be used on this page yet/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Use a passkey" })).toBeNull();
  });

  it("offers both methods in forced enrollment and shares its recovery-code acknowledgment", async () => {
    confirm.mockResolvedValueOnce(["ABCDE-FGHIJ"]);
    await mount(<MFASetupPage redirectTo="/" />);
    expect(await screen.findByRole("button", { name: "Use an authenticator app" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Use a passkey" }));
    fireEvent.click(await screen.findByRole("button", { name: "Create passkey" }));
    expect(await screen.findByRole("list", { name: "Recovery codes" })).toBeTruthy();
    expect(auth.reloadSession).not.toHaveBeenCalled();
    fireEvent.click(screen.getByLabelText("I've saved these codes"));
    fireEvent.click(screen.getByRole("button", { name: "Finish" }));
    await waitFor(() => expect(auth.reloadSession).toHaveBeenCalledOnce());
  });

  it("retries a fresh registration after passkey step-up instead of reusing a consumed ceremony", async () => {
    confirm.mockRejectedValueOnce(new AppError({ code: "mfa_reverify_required", message: "expired", httpStatus: 403 }));
    begin.mockResolvedValueOnce(enrollment).mockResolvedValueOnce({ ...enrollment, ceremonyId: "fresh" });
    const onEnrolled = vi.fn();
    await mount(<PasskeyEnrollmentForm canReverifyPasskey onEnrolled={onEnrolled} />);
    fireEvent.click(await screen.findByRole("button", { name: "Create passkey" }));
    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    await waitFor(() => expect(onEnrolled).toHaveBeenCalledWith(null));
    expect(reverify).toHaveBeenCalledWith(proof);
    expect(confirm.mock.calls.map(([input]) => input.ceremonyId)).toEqual(["registration", "fresh"]);
  });

  it("returns silently to enrollment after browser cancellation without confirmation", async () => {
    begin.mockResolvedValueOnce(null);
    await mount(<PasskeyEnrollmentForm onEnrolled={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Create passkey" });
    fireEvent.click(button);
    await waitFor(() => expect(button).toBe(document.activeElement));
    expect(confirm).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("explains that a second passkey needs another authenticator", async () => {
    begin.mockRejectedValueOnce(new DOMException("already enrolled", "InvalidStateError"));
    await mount(<PasskeyEnrollmentForm onEnrolled={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Create passkey" }));
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "This authenticator already has a passkey for this account. Use another device or security key.",
    );
    expect(confirm).not.toHaveBeenCalled();
  });

  it("does not submit a browser credential after its enrollment form unmounts", async () => {
    let resolveEnrollment: (value: typeof enrollment) => void = () => {};
    begin.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveEnrollment = resolve;
        }),
    );
    const onEnrolled = vi.fn();
    await mount(<PasskeyEnrollmentForm onEnrolled={onEnrolled} />);
    fireEvent.click(await screen.findByRole("button", { name: "Create passkey" }));
    cleanup();
    resolveEnrollment(enrollment);
    await waitFor(() => expect(begin.mock.calls[0]![0].signal.aborted).toBe(true));
    expect(confirm).not.toHaveBeenCalled();
    expect(onEnrolled).not.toHaveBeenCalled();
  });

  it("keeps forced enrollment busy while the session reload finishes without new recovery codes", async () => {
    let finishReload = () => {};
    vi.mocked(auth.reloadSession).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finishReload = resolve;
        }),
    );
    await mount(<MFASetupPage redirectTo="/" />);
    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    fireEvent.click(await screen.findByRole("button", { name: "Create passkey" }));
    await screen.findByText("Finishing…");
    expect((screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement).disabled).toBe(true);
    finishReload();
  });

  it("keeps one-time recovery codes visible until saved and refreshes the enrolled factor", async () => {
    let factors: MFAFactors = { factors: [], recoveryCodesRemaining: 0, requiredByPolicy: false };
    confirm.mockImplementationOnce(async () => {
      factors = {
        ...factors,
        factors: [
          {
            tenantOnly: false,
            id: "factor",
            type: "webauthn",
            label: "Laptop",
            createdAt: new Date().toISOString(),
            lastUsedAt: null,
          },
        ],
      };
      return ["ABCDE-FGHIJ"];
    });
    await mount(
      <TwoFactorSection
        client={{
          list: async () => factors,
          remove: vi.fn(),
          regenerate: vi.fn(),
          begin: vi.fn(),
          confirm: vi.fn(),
          reverify,
        }}
      />,
      { ...auth, tenant },
    );
    const trigger = await screen.findByRole("button", { name: "Add passkey" });
    trigger.focus();
    fireEvent.click(trigger);
    const sheet = await screen.findByRole("dialog", { name: "Add passkey" });
    fireEvent.change(within(sheet).getByLabelText("Name"), { target: { value: " Laptop " } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Create passkey" }));
    expect(await screen.findByRole("list", { name: "Recovery codes" })).toBeTruthy();
    expect(confirm).toHaveBeenCalledWith({ ...enrollment, label: "Laptop" });
    await screen.findByText("Laptop");
    fireEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    expect(screen.getByRole("dialog", { name: "Add passkey" })).toBeTruthy();
    fireEvent.click(screen.getByLabelText("I've saved these codes"));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add passkey" })).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });
});
