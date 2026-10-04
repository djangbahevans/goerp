import type { ContrastPreference, ThemePreference } from "../react/use-theme.js";

// auth-internals.md §2's user_profiles.date_format.
export type DateFormat = "day_first" | "month_first" | "iso";

export interface CurrentUser {
  id: string;
  email: string;
  contactId: string | null;
  name: string | null;
  avatarUrl: string | null;
  roles: string[];
  amr: string[];
  mfaVerifiedAt: string | null;
  // auth-internals.md §8 "MFA enrollment": the tenant requires MFA and the
  // user has no factor yet, so the shell holds them on /auth/mfa-setup.
  mfaSetupRequired: boolean;
  passwordChangeRequired: boolean;
  passwordMinLength: number;
  phone: string | null;
  title: string | null;
  theme: ThemePreference;
  contrast: ContrastPreference;
  // BCP 47; null = the tenant's defaultLocale.
  locale: string | null;
  // IANA; null = the tenant's defaultTimezone.
  timezone: string | null;
  // null = the locale's own date order.
  dateFormat: DateFormat | null;
}

export interface CurrentTenant {
  id: string;
  slug: string;
  name: string;
  plan: string;
  // l10n-guide.md §2 "Tenant default locale".
  defaultLocale: string;
  defaultTimezone: string;
  // The locales the Appearance page offers.
  availableLocales: string[];
  // The effective minimum password length (auth-internals.md §3
  // "Password strength validation").
  passwordMinLength: number;
}

export interface LoginCredentials {
  email: string;
  password: string;
  // Omitted on the shared-domain host: the engine finds the account's
  // tenants itself (auth-internals.md §3 "Cross-tenant user membership").
  tenant?: string | undefined;
  // "Remember this device" — a 30-day session instead of one that ends with
  // the browser session.
  remember?: boolean | undefined;
}

export interface PasswordResetRequest {
  email: string;
  // Omitted on the shared-domain host: the account's own tenants.
  tenant?: string | undefined;
}

export interface PasswordResetConfirmation {
  token: string;
  newPassword: string;
  tenant: string;
}

// "signed_in": the response set a session. "login_required": the password
// changed but no session was issued (MFA enrolled, or not an active member
// of the tenant), so the user signs in normally.
export type PasswordResetOutcome = "signed_in" | "login_required";

export interface EmailVerification {
  token: string;
  // From the verification link; empty when the link has none, which still
  // verifies but can't sign the user in.
  tenant: string;
}

// "signed_in": the response set a session. "login_required": the email is
// verified but no session was issued (MFA enrolled, or no resolvable tenant
// membership), so the user signs in normally.
export type EmailVerificationOutcome = "signed_in" | "login_required";

export interface Registration {
  name: string;
  email: string;
  password: string;
  companyName: string;
}

// signed_in: the account is active and the response set a session.
// login_required: the account and workspace exist but no session was
// issued. verification_required: the account must verify its email first.
// provisioning_pending: the workspace is still being set up.
// A sign-in on the shared-domain login host, handed to the tenant's own
// host, where /auth/handoff exchanges the code for the session
// (auth-internals.md §3 "Shared-domain handoff").
// A tenantless sign-in's 409 tenant_required: the account's tenants, and
// the single-use token that picks one of them.
export interface TenantSelection {
  tenants: { slug: string; name: string }[];
  selectionToken: string;
}

export interface SignInHandoff {
  host: string;
  code: string;
}

export type RegisterOutcome =
  | { kind: "signed_in" | "login_required" | "verification_required" | "provisioning_pending"; tenantSlug: string }
  | { kind: "handoff"; tenantSlug: string; handoff: SignInHandoff };

export interface VerificationEmailRequest {
  email: string;
  // Omitted on the shared-domain host: the account's only tenant.
  tenant?: string | undefined;
}

export interface InviteLink {
  token: string;
  tenant: string;
}

export interface InviteInfo {
  tenantName: string;
  email: string;
  name: string | null;
  // False when the invitee already has an account on another tenant: they
  // keep their existing password and just gain access.
  passwordRequired: boolean;
  passwordMinLength: number;
}

export interface InviteAcceptance extends InviteLink {
  // Only sent when InviteInfo.passwordRequired.
  password?: string | undefined;
}

// "signed_in": the response set a session. "login_required": access was
// granted but no session issued (an existing account), so the user signs in.
export type InviteAcceptOutcome = "signed_in" | "login_required";

// The pre-login tenant lookup (GET /auth/tenant-context). tenant is null on
// the shared-domain host, where the Host alone doesn't identify one, and
// when workspaceNotFound: the Host names no workspace at all.
export interface TenantContext {
  tenant: { slug: string; name: string } | null;
  registrationEnabled: boolean;
  // The terms of service registration requires accepting; null when the
  // platform configures none.
  termsUrl: string | null;
  // The app's URL on the shared-domain host.
  appUrl: string | null;
  workspaceNotFound: boolean;
  // The effective minimum password length: the tenant's, or the global one
  // on the shared-domain host.
  passwordMinLength: number;
}

export type MFAMethod = "totp" | "webauthn" | "recovery_code";

export type AuthState =
  | { status: "idle" }
  | { status: "checking" }
  | { status: "authenticated"; user: CurrentUser; tenant: CurrentTenant }
  | { status: "unauthenticated" }
  // The session ended without the user signing out (a refresh failed), so
  // the shell keeps the page under a sign-in-again modal (shell-ux.md §6.4).
  // It keeps the user and tenant it expired for, so the providers keyed on
  // them don't remount the page.
  | { status: "unauthenticated"; sessionExpired: true; user: CurrentUser; tenant: CurrentTenant }
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
  | { type: "logout_complete" }
  | { type: "profile_updated"; user: CurrentUser }
  | { type: "mfa_setup_required" }
  | { type: "password_change_required" }
  | { type: "session_reloaded"; user: CurrentUser; tenant: CurrentTenant };

export interface ChangePasswordInput {
  currentPassword: string;
  newPassword: string;
}

export interface UpdateProfileInput {
  name: string;
  phone?: string | null | undefined;
  title?: string | null | undefined;
  avatarId?: string | undefined;
}

// updatePreferences' input: only the fields given are sent, and null resets
// locale, timezone or dateFormat to inherit.
export type UpdatePreferencesInput = Partial<
  Pick<CurrentUser, "theme" | "contrast" | "locale" | "timezone" | "dateFormat">
>;

export interface AuthContextValue {
  state: AuthState;
  isAuthenticated: boolean;
  user: CurrentUser | null;
  tenant: CurrentTenant | null;
  // Resolves to a handoff when the sign-in must continue on the tenant's
  // own host, null when it completed (or reached MFA) here.
  login: (credentials: LoginCredentials) => Promise<SignInHandoff | null>;
  // Exchanges a handoff code on the tenant's host, then continues as login
  // does: signed in, or at the MFA challenge.
  completeHandoff: (code: string) => Promise<void>;
  // Finishes a tenantless sign-in for the chosen tenant (POST
  // /auth/select-tenant); resolves like login.
  selectTenant: (selectionToken: string, tenant: string) => Promise<SignInHandoff | null>;
  logout: () => Promise<void>;
  submitMFA: (confirmation: MFAVerification) => Promise<void>;
  updateProfile: (input: UpdateProfileInput) => Promise<void>;
  // PATCH /auth/me with the Appearance preferences (shell-ux.md §4.4).
  // Rejects with an AppError whose code is invalid_preference and whose
  // details.field names the rejected field.
  updatePreferences: (input: UpdatePreferencesInput) => Promise<void>;
  changePassword: (input: ChangePasswordInput) => Promise<void>;
  // Re-reads GET /auth/me into the signed-in state, e.g. once MFA setup finishes.
  reloadSession: () => Promise<void>;
  // Moves a signed-in session to the expired state, keeping its user and
  // tenant, for an action that just revoked the caller's own session.
  expireSession: () => void;
}

// One entry of GET /auth/sessions (auth-internals.md §4 "Session management
// endpoints"): a sign-in, identified by its session family.
export interface ActiveSession {
  id: string;
  // Raw and unparsed; the shell labels it "browser on OS".
  userAgent: string | null;
  ipAddress: string | null;
  // ISO 3166-1 alpha-2.
  countryCode: string | null;
  signedInAt: string;
  lastActiveAt: string;
  persistent: boolean;
  current: boolean;
}

// POST /auth/mfa/enroll/totp (auth-internals.md §8 "MFA enrollment").
export interface TOTPEnrollment {
  enrollmentId: string;
  qrSvg: string;
  // Base32 manual-entry key for authenticator apps that can't scan.
  secret: string;
}

export interface TOTPEnrollmentConfirmation {
  enrollmentId: string;
  code: string;
  label?: string | undefined;
}

// One enrolled sign-in factor from GET /auth/mfa/factors (auth-internals.md
// §8 "Managing factors"). Recovery codes are counted, not listed.
export interface MFAFactor {
  tenantOnly: boolean;
  id: string;
  type: "totp" | "webauthn";
  label: string | null;
  createdAt: string;
  lastUsedAt: string | null;
}

export interface MFAFactors {
  factors: MFAFactor[];
  recoveryCodesRemaining: number;
  // Whether the tenant's MFA policy applies to this user, so their last
  // factor can't be removed.
  requiredByPolicy: boolean;
}

// A current MFA code confirming a factor change or re-verification.
export interface MFACodeConfirmation {
  type: Extract<MFAMethod, "totp" | "recovery_code">;
  code: string;
}

export interface PasskeyEnrollment {
  ceremonyId: string;
  response: RegistrationResponseJSON;
}

export interface PasskeyEnrollmentConfirmation extends PasskeyEnrollment {
  label?: string | undefined;
}

export interface MFAPasskeyConfirmation {
  type: "webauthn";
  ceremonyId: string;
  response: AuthenticationResponseJSON;
}

export type MFAVerification = MFACodeConfirmation | MFAPasskeyConfirmation;
