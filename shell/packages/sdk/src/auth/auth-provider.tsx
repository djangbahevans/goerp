import { createContext, useCallback, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";

import { fetchCurrentSession, login as loginRequest, logout as logoutRequest, submitMFACode } from "./auth-client.js";
import { authMachine } from "./auth-machine.js";
import type { AuthContextValue, LoginCredentials, MFAMethod } from "./types.js";

export const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const state = useSyncExternalStore(authMachine.subscribe, authMachine.getState);

  // Mount-time session check — idle → checking → authenticated/unauthenticated.
  useEffect(() => {
    if (state.status !== "idle") return;
    authMachine.transition({ type: "check_session" });
    void fetchCurrentSession().then((session) => {
      if (session) {
        authMachine.transition({ type: "session_checked", user: session.user, tenant: session.tenant });
      } else {
        authMachine.transition({ type: "session_check_failed" });
      }
    });
  }, [state.status]);

  const login = useCallback(async (credentials: LoginCredentials): Promise<void> => {
    authMachine.transition({ type: "login_started" });

    const result = await loginRequest(credentials).catch((err: unknown) => {
      authMachine.transition({ type: "login_failed" });
      throw err;
    });

    if (result.kind === "mfa_required") {
      authMachine.transition({
        type: "login_requires_mfa",
        challengeToken: result.challengeToken,
        methods: result.methods,
      });
      return;
    }

    const session = await fetchCurrentSession();
    if (!session) {
      authMachine.transition({ type: "login_failed" });
      throw new Error("login succeeded but the session check that follows it failed");
    }
    authMachine.transition({ type: "login_succeeded", user: session.user, tenant: session.tenant });
  }, []);

  const submitMFA = useCallback(async (code: string, method: MFAMethod = "totp"): Promise<void> => {
    const current = authMachine.getState();
    if (current.status !== "mfa_required") {
      throw new Error("submitMFA called outside the mfa_required state");
    }
    const challengeToken = current.challengeToken;

    await submitMFACode(challengeToken, code, method).catch((err: unknown) => {
      authMachine.transition({ type: "mfa_failed" });
      throw err;
    });

    const session = await fetchCurrentSession();
    if (!session) {
      authMachine.transition({ type: "mfa_failed" });
      throw new Error("mfa verification succeeded but the session check that follows it failed");
    }
    authMachine.transition({ type: "mfa_verified", user: session.user, tenant: session.tenant });
  }, []);

  const logout = useCallback(async (): Promise<void> => {
    authMachine.transition({ type: "logout_started" });
    await logoutRequest();
    authMachine.transition({ type: "logout_complete" });
  }, []);

  const value = useMemo<AuthContextValue>(() => {
    const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
    return {
      state,
      isAuthenticated,
      user: isAuthenticated ? state.user : null,
      tenant: isAuthenticated ? state.tenant : null,
      login,
      logout,
      submitMFA,
    };
  }, [state, login, logout, submitMFA]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
