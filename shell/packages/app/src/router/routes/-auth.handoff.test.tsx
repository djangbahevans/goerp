import type { AuthContextValue, AuthState } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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

type HandoffImpl = (code: string, setState: (state: AuthState) => void) => Promise<void>;

function FakeAuthProvider({ handoffImpl, children }: { handoffImpl: HandoffImpl; children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "unauthenticated" });
  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  const value: AuthContextValue = {
    state,
    isAuthenticated,
    user: isAuthenticated ? state.user : null,
    tenant: isAuthenticated ? state.tenant : null,
    login: async () => null,
    completeHandoff: (code) => handoffImpl(code, setState),
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences: async () => {},
    changePassword: async () => {},
    reloadSession: async () => {},
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

async function renderHandoff(url: string, handoffImpl: HandoffImpl) {
  const history = createMemoryHistory({ initialEntries: [url] });
  const router = createRouter({ routeTree, context: { auth: undefined! }, history });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <FakeAuthProvider handoffImpl={handoffImpl}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </FakeAuthProvider>
    </QueryClientProvider>,
  );
  return router;
}

afterEach(() => {
  cleanup();
});

describe("/auth/handoff", () => {
  it("exchanges the code once, drops it from the URL, then goes to ?redirect", async () => {
    let resolve: () => void = () => {};
    const handoffImpl = vi.fn<HandoffImpl>(
      (_code, setState) =>
        new Promise((r) => {
          resolve = () => {
            setState({ status: "authenticated", user: FAKE_USER, tenant: FAKE_TENANT });
            r();
          };
        }),
    );
    const router = await renderHandoff("/auth/handoff?code=c0de&redirect=%2Fsettings%2Fprofile", handoffImpl);

    expect(await screen.findByRole("status")).toBeTruthy();
    await waitFor(() => expect(handoffImpl).toHaveBeenCalledWith("c0de", expect.any(Function)));
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("code"));

    resolve();
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/profile"));
    expect(handoffImpl).toHaveBeenCalledTimes(1);
  });

  it("goes to the MFA challenge when the exchange asks for a second factor", async () => {
    const router = await renderHandoff("/auth/handoff?code=c0de", async (_code, setState) => {
      setState({ status: "mfa_required", challengeToken: "tok", methods: ["totp"] });
    });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/mfa"));
  });

  it("sends the user back to sign in when the code is rejected", async () => {
    const router = await renderHandoff("/auth/handoff?code=spent", async () => {
      throw new AppError({
        code: "auth.handoff_code_invalid",
        message: "sign-in handoff expired or already used",
        httpStatus: 401,
      });
    });

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(router.state.location.search).toMatchObject({ notice: "session_failed" });
  });

  it("sends the user back to sign in without a code", async () => {
    const handoffImpl = vi.fn<HandoffImpl>(async () => {});
    const router = await renderHandoff("/auth/handoff", handoffImpl);

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(handoffImpl).not.toHaveBeenCalled();
  });
});
