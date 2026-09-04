export interface CurrentUser {
  id: string;
  email: string;
  roles: string[];
  amr: string[];
  mfaVerifiedAt: string | null;
}

export interface CurrentTenant {
  id: string;
  slug: string;
  name: string;
  plan: string;
}

export interface LoginCredentials {
  email: string;
  password: string;
  tenant: string;
}

export type MFAMethod = "totp" | "webauthn" | "recovery_code";

export type AuthState =
  | { status: "idle" }
  | { status: "checking" }
  | { status: "authenticated"; user: CurrentUser; tenant: CurrentTenant }
  | { status: "unauthenticated" }
  | { status: "refreshing"; user: CurrentUser; tenant: CurrentTenant }
  | { status: "mfa_required"; challengeToken: string; methods: MFAMethod[] }
  | { status: "logging_out" };

export type AuthEvent =
  | { type: "check_session" }
  | { type: "session_checked"; user: CurrentUser; tenant: CurrentTenant }
  | { type: "session_check_failed" }
  | { type: "login_started" }
  | { type: "login_succeeded"; user: CurrentUser; tenant: CurrentTenant }
  | { type: "login_requires_mfa"; challengeToken: string; methods: MFAMethod[] }
  | { type: "login_failed" }
  | { type: "mfa_verified"; user: CurrentUser; tenant: CurrentTenant }
  | { type: "mfa_failed" }
  | { type: "refresh_started" }
  | { type: "refresh_succeeded" }
  | { type: "refresh_failed" }
  | { type: "session_expired" }
  | { type: "logout_started" }
  | { type: "logout_complete" };

export interface AuthContextValue {
  state: AuthState;
  isAuthenticated: boolean;
  user: CurrentUser | null;
  tenant: CurrentTenant | null;
  login: (credentials: LoginCredentials) => Promise<void>;
  logout: () => Promise<void>;
  submitMFA: (code: string, method?: MFAMethod) => Promise<void>;
}
