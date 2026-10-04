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
  selectTenant as selectTenantRequest,
  updatePreferences as updatePreferencesRequest,
  updateProfile as updateProfileRequest,
  verifyMFA,
} from "./auth-client.js";
import { authMachine, sessionIdentity } from "./auth-machine.js";
import { passwordUpdateNotice } from "./password-update-notice.js";
import type {
  AuthContextValue,
  ChangePasswordInput,
  CurrentTenant,
  CurrentUser,
  LoginCredentials,
  MFAVerification,
  SignInHandoff,
  UpdatePreferencesInput,
  UpdateProfileInput,
} from "./types.js";

export const AuthContext = createContext<AuthContextValue | null>(null);

// shell-ux.md §4.4: the profile's theme and contrast replace the local ones when a
// session starts, and the locale follows user → tenant default
// (l10n-guide.md §2), so both carry across devices.
function applySessionPreferences(session: { user: CurrentUser; tenant: CurrentTenant }): void {
  themeStore.setPreference(session.user.theme);
  themeStore.setContrastPreference(session.user.contrast);
  try {
    localeStore.setLocale(session.user.locale ?? session.tenant.defaultLocale);
  } catch {
    // An unparseable locale keeps the current one.
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const state = useSyncExternalStore(authMachine.subscribe, authMachine.getState);

  // The machine transition rejects the second StrictMode effect before it can issue another session request.
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

  useEffect(() => {
    if (state.status === "unauthenticated") passwordUpdateNotice.set(false);
  }, [state.status]);

  const signIn = useCallback(async (request: () => Promise<LoginResult>): Promise<SignInHandoff | null> => {
    if (!authMachine.transition({ type: "login_started" })) {
      throw new Error("login() called while the auth machine wasn't unauthenticated or mfa_required");
    }

    const result = await request().catch((err: unknown) => {
      authMachine.transition({ type: "login_failed" });
      throw err;
    });

    if (result.kind === "handoff") {
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

    passwordUpdateNotice.set(result.passwordUpdateRecommended, result.passwordUpdateDeadline);
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

  const selectTenant = useCallback(
    (selectionToken: string, tenant: string): Promise<SignInHandoff | null> =>
      signIn(() => selectTenantRequest(selectionToken, tenant)),
    [signIn],
  );

  // Concurrent verification can consume the single-use token before the first response updates auth state.
  const mfaInFlight = useRef(false);

  const submitMFA = useCallback(async (confirmation: MFAVerification): Promise<void> => {
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
        const notice = await verifyMFA(challengeToken, confirmation);
        passwordUpdateNotice.set(notice.recommended, notice.deadline);
      } catch (err) {
        // A server rejection spends the MFA token; a request that never reaches the server leaves it usable.
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

  const expireSession = useCallback((): void => {
    authMachine.transition({ type: "session_expired" });
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
      selectTenant,
      logout,
      submitMFA,
      updateProfile,
      updatePreferences,
      changePassword,
      reloadSession,
      expireSession,
    };
  }, [
    state,
    login,
    completeHandoff,
    selectTenant,
    logout,
    submitMFA,
    updateProfile,
    updatePreferences,
    changePassword,
    reloadSession,
    expireSession,
  ]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
