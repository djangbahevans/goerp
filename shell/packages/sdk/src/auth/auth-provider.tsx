import { createContext, type ReactNode, useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { AppError } from "../error/app-error.js";
import {
  changePassword as changePasswordRequest,
  fetchCurrentSession,
  login as loginRequest,
  logout as logoutRequest,
  submitMFACode,
  updateProfile as updateProfileRequest,
} from "./auth-client.js";
import { authMachine } from "./auth-machine.js";
import { passwordUpdateNotice } from "./password-update-notice.js";
import type {
  AuthContextValue,
  ChangePasswordInput,
  LoginCredentials,
  MFAMethod,
  UpdateProfileInput,
} from "./types.js";

export const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const state = useSyncExternalStore(authMachine.subscribe, authMachine.getState);

  // Mount-time session check — idle → checking → authenticated/unauthenticated.
  // check_session only ever applies once (nothing transitions back into
  // "idle"), so gating on its own return value — rather than a stale
  // `state` read from the render that scheduled this effect — is also
  // what makes StrictMode's dev-mode double-invocation a no-op instead of
  // firing GET /auth/me twice: the second invocation's transition call
  // reads the machine's real current state ("checking" by then) and is
  // rejected.
  useEffect(() => {
    if (!authMachine.transition({ type: "check_session" })) return;
    void fetchCurrentSession().then((session) => {
      if (session) {
        authMachine.transition({ type: "session_checked", user: session.user, tenant: session.tenant });
      } else {
        authMachine.transition({ type: "session_check_failed" });
      }
    });
  }, []);

  // Logout, an expired session and a failed MFA challenge all end here.
  useEffect(() => {
    if (state.status === "unauthenticated") passwordUpdateNotice.set(false);
  }, [state.status]);

  const login = useCallback(async (credentials: LoginCredentials): Promise<void> => {
    // login_started only applies from "unauthenticated" (or an abandoned
    // "mfa_required" challenge) — rejects outright
    // if the mount-time session check (or another login) hasn't finished,
    // instead of proceeding to race its own session check against it.
    if (!authMachine.transition({ type: "login_started" })) {
      throw new Error("login() called while the auth machine wasn't unauthenticated or mfa_required");
    }

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

    // Every sign-in writes the flag, set or cleared, so a later user in this
    // tab never inherits it.
    passwordUpdateNotice.set(result.passwordUpdateRecommended);
    const session = await fetchCurrentSession();
    if (!session) {
      authMachine.transition({ type: "login_failed" });
      throw new Error("login succeeded but the session check that follows it failed");
    }
    authMachine.transition({ type: "login_succeeded", user: session.user, tenant: session.tenant });
  }, []);

  // mfaInFlight guards against a double-submit racing two verify calls for
  // the same challenge: auth-internals.md §8 step 4 consumes the mfa_token
  // atomically on whichever request reaches the server first, so a second
  // concurrent call would otherwise be rejected as already-consumed and
  // could report failure before the first call's own success is applied.
  const mfaInFlight = useRef(false);

  const submitMFA = useCallback(async (code: string, method: MFAMethod = "totp"): Promise<void> => {
    if (mfaInFlight.current) {
      throw new Error("submitMFA() is already in progress");
    }
    const current = authMachine.getState();
    if (current.status !== "mfa_required") {
      throw new Error("submitMFA called outside the mfa_required state");
    }
    const challengeToken = current.challengeToken;

    mfaInFlight.current = true;
    try {
      try {
        passwordUpdateNotice.set(await submitMFACode(challengeToken, code, method));
      } catch (err) {
        // The mfa_token is only consumed once the server actually
        // receives and processes the request (auth-internals.md §8 step
        // 4 runs before code verification, so this holds for a wrong
        // code too) — a network failure that never reached the server
        // leaves it valid, so only a genuine server rejection (AppError)
        // forces the unauthenticated/fresh-login path.
        if (err instanceof AppError) {
          authMachine.transition({ type: "mfa_failed" });
        }
        throw err;
      }

      const session = await fetchCurrentSession();
      if (!session) {
        authMachine.transition({ type: "mfa_failed" });
        throw new Error("mfa verification succeeded but the session check that follows it failed");
      }
      authMachine.transition({ type: "mfa_verified", user: session.user, tenant: session.tenant });
    } finally {
      mfaInFlight.current = false;
    }
  }, []);

  const updateProfile = useCallback(async (input: UpdateProfileInput): Promise<void> => {
    const current = authMachine.getState();
    if (current.status !== "authenticated" && current.status !== "refreshing") {
      throw new Error("updateProfile called outside the authenticated state");
    }

    await updateProfileRequest(input);

    const session = await fetchCurrentSession();
    if (!session) {
      throw new Error("updateProfile succeeded but the session check that follows it failed");
    }
    authMachine.transition({ type: "profile_updated", user: session.user });
  }, []);

  const changePassword = useCallback(async (input: ChangePasswordInput): Promise<void> => {
    const current = authMachine.getState();
    if (current.status !== "authenticated" && current.status !== "refreshing") {
      throw new Error("changePassword called outside the authenticated state");
    }
    await changePasswordRequest(input);
    passwordUpdateNotice.set(false);
  }, []);

  const reloadSession = useCallback(async (): Promise<void> => {
    const current = authMachine.getState();
    if (current.status !== "authenticated" && current.status !== "refreshing") {
      throw new Error("reloadSession called outside the authenticated state");
    }
    const session = await fetchCurrentSession();
    if (!session) {
      throw new Error("reloadSession's session check failed");
    }
    authMachine.transition({ type: "session_reloaded", user: session.user, tenant: session.tenant });
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
      updateProfile,
      changePassword,
      reloadSession,
    };
  }, [state, login, logout, submitMFA, updateProfile, changePassword, reloadSession]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
