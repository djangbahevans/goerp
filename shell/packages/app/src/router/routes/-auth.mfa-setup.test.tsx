import type { AuthContextValue, AuthState, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatManualKey } from "../../auth/mfa-setup-page.js";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const USER: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: true,
};
const TENANT = { id: "t1", slug: "acme", name: "Acme Corp", plan: "pro" };
const CODES = Array.from({ length: 10 }, (_, i) => `AAAA${i}-BBBBB`);

type ConfirmReply = { status: number; body: unknown };

// Stateful stand-in for AuthProvider: reloadSession clears the setup flag the
// way GET /auth/me does once enrollment is confirmed.
function FakeAuthProvider({
  initial,
  reloadSession,
  children,
}: {
  initial: AuthState;
  reloadSession: () => void;
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
    logout: async () => setState({ status: "unauthenticated" }),
    submitMFA: async () => {},
    updateProfile: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {
      reloadSession();
      if (state.status === "authenticated") {
        setState({ ...state, user: { ...state.user, mfaSetupRequired: false } });
      }
    },
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

async function renderSetup({
  url = "/auth/mfa-setup?redirect=%2Fsettings%2Fprofile",
  initial = { status: "authenticated", user: USER, tenant: TENANT } as AuthState,
  confirmReplies = [{ status: 200, body: { recovery_codes: CODES, expires_in: 900 } }] as ConfirmReply[],
  reloadFailures = 0,
}: {
  url?: string;
  initial?: AuthState;
  confirmReplies?: ConfirmReply[];
  reloadFailures?: number;
} = {}) {
  let enrollments = 0;
  const confirmBodies: unknown[] = [];
  const replies = [...confirmReplies];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/auth/mfa/enroll/totp") {
        enrollments += 1;
        return new Response(
          JSON.stringify({ enrollment_id: `e${enrollments}`, qr_svg: "<svg/>", secret: "JBSWY3DPEHPK3PXP" }),
          { status: 200 },
        );
      }
      if (url === "/auth/mfa/enroll/totp/confirm") {
        confirmBodies.push(JSON.parse(String(init?.body)));
        const reply = replies.shift() ?? { status: 500, body: {} };
        return new Response(JSON.stringify(reply.body), { status: reply.status });
      }
      return new Response(JSON.stringify({ tenant: null, registration_enabled: false }), { status: 200 });
    }),
  );
  const reloadSession = vi.fn();
  for (let i = 0; i < reloadFailures; i++) {
    reloadSession.mockImplementationOnce(() => {
      throw new Error("session check failed");
    });
  }
  const router = createRouter({
    routeTree,
    context: { auth: undefined! },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider initial={initial} reloadSession={reloadSession}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  return { router, reloadSession, confirmBodies, enrollmentCount: () => enrollments };
}

function codeInput(): HTMLInputElement {
  return screen.getByLabelText("Verification code") as HTMLInputElement;
}

function invalidCode(): ConfirmReply {
  return { status: 400, body: { error: { code: "invalid_mfa_code", message: "invalid MFA code" } } };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("formatManualKey", () => {
  it("groups the key in fours", () => {
    expect(formatManualKey("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
  });
});

describe("/auth/mfa-setup", () => {
  it("scans, confirms, shows recovery codes, and finishes to ?redirect", async () => {
    const { router, reloadSession, confirmBodies } = await renderSetup();

    expect(await screen.findByRole("img", { name: /QR code/ })).toBeTruthy();
    expect(screen.getByText("JBSW Y3DP EHPK 3PXP")).toBeTruthy();
    fireEvent.change(codeInput(), { target: { value: "123456" } });

    expect(await screen.findByRole("list", { name: "Recovery codes" })).toBeTruthy();
    expect(screen.getAllByRole("listitem")).toHaveLength(10);
    expect(confirmBodies).toEqual([{ enrollment_id: "e1", code: "123456" }]);

    const finish = screen.getByRole("button", { name: "Finish" }) as HTMLButtonElement;
    expect(finish.disabled).toBe(true);
    fireEvent.click(screen.getByLabelText("I've saved these codes"));
    expect(finish.disabled).toBe(false);
    fireEvent.click(finish);

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(reloadSession).toHaveBeenCalledTimes(1);
  });

  it("shows an inline error for a wrong code and stays on step 1", async () => {
    await renderSetup({ confirmReplies: [invalidCode()] });
    await screen.findByLabelText("Verification code");

    fireEvent.change(codeInput(), { target: { value: "000000" } });

    expect(await screen.findByText("Incorrect code. Check your authenticator app and try again.")).toBeTruthy();
    expect(screen.queryByRole("list", { name: "Recovery codes" })).toBeNull();
  });

  it("restarts with a fresh enrollment when the pending one has expired", async () => {
    const { enrollmentCount, confirmBodies } = await renderSetup({
      confirmReplies: [
        { status: 404, body: { error: { code: "mfa_enrollment_not_found", message: "gone" } } },
        { status: 200, body: { recovery_codes: CODES } },
      ],
    });
    await screen.findByLabelText("Verification code");
    fireEvent.change(codeInput(), { target: { value: "111111" } });

    expect(await screen.findByText(/That setup expired/)).toBeTruthy();
    expect(enrollmentCount()).toBe(2);
    fireEvent.change(codeInput(), { target: { value: "222222" } });
    await screen.findByRole("list", { name: "Recovery codes" });
    expect(confirmBodies.at(-1)).toEqual({ enrollment_id: "e2", code: "222222" });
  });

  it("finishes straight after confirm when no recovery codes come back", async () => {
    const { router, reloadSession } = await renderSetup({
      confirmReplies: [{ status: 200, body: { recovery_codes: null } }],
    });
    await screen.findByLabelText("Verification code");
    fireEvent.change(codeInput(), { target: { value: "123456" } });

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(reloadSession).toHaveBeenCalledTimes(1);
  });

  it("offers a retry when finishing fails after a confirm with no new codes", async () => {
    const { router, reloadSession } = await renderSetup({
      confirmReplies: [{ status: 200, body: { recovery_codes: null } }],
      reloadFailures: 1,
    });
    await screen.findByLabelText("Verification code");
    fireEvent.change(codeInput(), { target: { value: "123456" } });

    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(reloadSession).toHaveBeenCalledTimes(2);
  });

  it("sends a signed-out visitor to /auth/login, keeping ?redirect", async () => {
    const { router } = await renderSetup({ initial: { status: "unauthenticated" } });
    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toEqual({ redirect: "/settings/profile" });
  });

  it("sends a user who no longer needs setup on to ?redirect", async () => {
    const { router } = await renderSetup({
      initial: { status: "authenticated", user: { ...USER, mfaSetupRequired: false }, tenant: TENANT },
    });
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
  });
});
