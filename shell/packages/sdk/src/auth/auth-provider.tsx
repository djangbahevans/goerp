import { createContext, type ReactNode, useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { AppError } from "../error/app-error.js";
import { localeStore } from "../i18n/use-locale.js";
import { themeStore } from "../react/use-theme.js";
import {
  changePassword as changePasswordRequest,
  exchangeHandoff,
  fetchCurrentSession,
  type LoginResult,
  login as loginRequest,
  logout as logoutRequest,
  submitMFACode,
  updatePreferences as updatePreferencesRequest,
  updateProfile as updateProfileRequest,
} from "./auth-client.js";
import { authMachine, sessionIdentity } from "./auth-machine.js";
import { passwordUpdateNotice } from "./password-update-notice.js";
import type {
  AuthContextValue,
  ChangePasswordInput,
  CurrentTenant,
  CurrentUser,
  LoginCredentials,
  MFAMethod,
  SignInHandoff,
  UpdatePreferencesInput,
  UpdateProfileInput,
} from "./types.js";

export const AuthContext = createContext<AuthContextValue | null>(null);

// shell-ux.md §4.4: the profile's theme replaces the local one when a
// session starts, and the locale follows user → tenant default
// (l10n-guide.md §2), so both carry across devices.
function applySessionPreferences(session: { user: CurrentUser; tenant: CurrentTenant }): void {
  themeStore.setPreference(session.user.theme);
  try {
    localeStore.setLocale(session.user.locale ?? session.tenant.defaultLocale);
  } catch {
    // An unparseable locale keeps the current one.
  }
}

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
        applySessionPreferences(session);
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

  // signIn runs one sign-in request through the auth machine: a login, or
  // a handoff exchange on the tenant's host. It resolves to the handoff
  // when the sign-in must continue on another host.
  const signIn = useCallback(async (request: () => Promise<LoginResult>): Promise<SignInHandoff | null> => {
    // login_started only applies from "unauthenticated" (or an abandoned
    // "mfa_required" challenge) — rejects outright
    // if the mount-time session check (or another login) hasn't finished,
    // instead of proceeding to race its own session check against it.
    if (!authMachine.transition({ type: "login_started" })) {
      throw new Error("login() called while the auth machine wasn't unauthenticated or mfa_required");
    }

    const result = await request().catch((err: unknown) => {
      authMachine.transition({ type: "login_failed" });
      throw err;
    });

    if (result.kind === "handoff") {
      // No session here; the tenant's host completes the sign-in.
      authMachine.transition({ type: "login_failed" });
      return result.handoff;
    }
    if (result.kind === "mfa_required") {
      authMachine.transition({
        type: "login_requires_mfa",
        challengeToken: result.challengeToken,
        methods: result.methods,
      });
      return null;
    }

    // Every sign-in writes the flag, set or cleared, so a later user in this
    // tab never inherits it.
    passwordUpdateNotice.set(result.passwordUpdateRecommended);
    const session = await fetchCurrentSession();
    if (!session) {
      authMachine.transition({ type: "login_failed" });
      throw new Error("login succeeded but the session check that follows it failed");
    }
    applySessionPreferences(session);
    authMachine.transition({ type: "login_succeeded", user: session.user, tenant: session.tenant });
    return null;
  }, []);

  const login = useCallback(
    (credentials: LoginCredentials): Promise<SignInHandoff | null> => signIn(() => loginRequest(credentials)),
    [signIn],
  );

  const completeHandoff = useCallback(
    async (code: string): Promise<void> => {
      await signIn(() => exchangeHandoff(code));
    },
    [signIn],
  );

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
      applySessionPreferences(session);
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

  const updatePreferences = useCallback(async (input: UpdatePreferencesInput): Promise<void> => {
    const current = authMachine.getState();
    if (current.status !== "authenticated" && current.status !== "refreshing") {
      throw new Error("updatePreferences called outside the authenticated state");
    }

    await updatePreferencesRequest(input);

    // The preferences are saved at this point, so a failed re-read keeps
    // the current session rather than reporting the save as failed.
    const session = await fetchCurrentSession();
    if (session) authMachine.transition({ type: "profile_updated", user: session.user });
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
    // An expired session keeps its user and tenant, so the page under the
    // session-expired modal (and anything keyed on them) stays as it was.
    const identity = sessionIdentity(state);
    return {
      state,
      isAuthenticated,
      user: identity?.user ?? null,
      tenant: identity?.tenant ?? null,
      login,
      completeHandoff,
      logout,
      submitMFA,
      updateProfile,
      updatePreferences,
      changePassword,
      reloadSession,
    };
  }, [
    state,
    login,
    completeHandoff,
    logout,
    submitMFA,
    updateProfile,
    updatePreferences,
    changePassword,
    reloadSession,
  ]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
