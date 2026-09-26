import { AppError } from "../error/app-error.js";
import type { ThemePreference } from "../react/use-theme.js";
import { noteTenantSuspension } from "./tenant-suspension.js";
import type {
  ChangePasswordInput,
  CurrentTenant,
  CurrentUser,
  DateFormat,
  EmailVerification,
  EmailVerificationOutcome,
  InviteAcceptance,
  InviteAcceptOutcome,
  InviteInfo,
  InviteLink,
  LoginCredentials,
  MFAMethod,
  PasswordResetConfirmation,
  PasswordResetOutcome,
  PasswordResetRequest,
  RegisterOutcome,
  Registration,
  SignInHandoff,
  TenantContext,
  TOTPEnrollment,
  TOTPEnrollmentConfirmation,
  UpdatePreferencesInput,
  UpdateProfileInput,
  VerificationEmailRequest,
} from "./types.js";

interface MeResponseBody {
  user: {
    id: string;
    email: string;
    contact_id: string | null;
    name: string | null;
    avatar_url: string | null;
    roles: string[];
    amr: string[];
    mfa_verified_at: string | null;
    mfa_setup_required?: boolean;
    theme: ThemePreference;
    locale: string | null;
    timezone: string | null;
    date_format: DateFormat | null;
  };
  tenant: {
    id: string;
    slug: string;
    name: string;
    plan: string;
    default_locale: string;
    default_timezone: string;
    available_locales: string[];
  };
}

function mapUser(user: MeResponseBody["user"]): CurrentUser {
  return {
    id: user.id,
    email: user.email,
    contactId: user.contact_id,
    name: user.name,
    avatarUrl: user.avatar_url,
    roles: user.roles,
    amr: user.amr,
    mfaVerifiedAt: user.mfa_verified_at,
    mfaSetupRequired: user.mfa_setup_required === true,
    theme: user.theme,
    locale: user.locale,
    timezone: user.timezone,
    dateFormat: user.date_format,
  };
}

function mapTenant(tenant: MeResponseBody["tenant"]): CurrentTenant {
  return {
    id: tenant.id,
    slug: tenant.slug,
    name: tenant.name,
    plan: tenant.plan,
    defaultLocale: tenant.default_locale,
    defaultTimezone: tenant.default_timezone,
    availableLocales: tenant.available_locales,
  };
}

// A 429's Retry-After header (whole seconds) surfaces as
// details.retryAfter, so a caller can show a lockout countdown.
async function readError(response: Response): Promise<AppError> {
  let code = "unknown_error";
  let message = response.statusText || "request failed";
  let bodyDetails: Record<string, unknown> | null = null;
  try {
    const body = (await response.json()) as { error?: { code?: string; message?: string; details?: unknown } };
    if (body.error?.code) code = body.error.code;
    if (body.error?.message) message = body.error.message;
    const d = body.error?.details;
    if (d !== null && typeof d === "object" && !Array.isArray(d)) bodyDetails = d as Record<string, unknown>;
  } catch {
    // Non-JSON or empty body — fall back to the status text above.
  }
  const retryAfter = Number.parseInt(response.headers.get("Retry-After") ?? "", 10);
  const details =
    bodyDetails || Number.isFinite(retryAfter)
      ? { ...bodyDetails, ...(Number.isFinite(retryAfter) ? { retryAfter } : {}) }
      : null;
  const error = new AppError({ code, message, httpStatus: response.status, details });
  noteTenantSuspension(error);
  return error;
}

// fetchCurrentSession backs the checking state (GET /auth/me,
// auth-internals.md §9). Any non-200 response — 401, or anything else —
// resolves to "no session" rather than throwing: the auth machine has no
// error state for this check to land in, only authenticated/unauthenticated.
export async function fetchCurrentSession(): Promise<{ user: CurrentUser; tenant: CurrentTenant } | null> {
  let response: Response;
  try {
    response = await fetch("/auth/me", { credentials: "include" });
  } catch {
    return null;
  }
  if (!response.ok) {
    if (response.status === 403) await readError(response);
    return null;
  }
  try {
    const body = (await response.json()) as MeResponseBody;
    return { user: mapUser(body.user), tenant: mapTenant(body.tenant) };
  } catch {
    return null;
  }
}

// fetchTenantContext backs GET /auth/tenant-context. Any failure resolves
// to null — the login page then falls back to asking for the company slug,
// the same form a shared-domain deployment gets.
export async function fetchTenantContext(): Promise<TenantContext | null> {
  try {
    const response = await fetch("/auth/tenant-context", { credentials: "include" });
    if (!response.ok) {
      if (response.status === 403) await readError(response);
      return null;
    }
    const body = (await response.json()) as {
      tenant: { slug: string; name: string } | null;
      registration_enabled: boolean;
      terms_url?: string | null;
    };
    return {
      tenant: body.tenant ? { slug: body.tenant.slug, name: body.tenant.name } : null,
      registrationEnabled: body.registration_enabled === true,
      termsUrl: typeof body.terms_url === "string" && body.terms_url !== "" ? body.terms_url : null,
    };
  } catch {
    return null;
  }
}

export type LoginResult =
  | { kind: "authenticated"; passwordUpdateRecommended: boolean }
  | { kind: "mfa_required"; challengeToken: string; methods: MFAMethod[] }
  | { kind: "handoff"; handoff: SignInHandoff };

type LoginResponseBody = {
  handoff?: SignInHandoff;
  mfa_required?: boolean;
  mfa_token?: string;
  mfa_methods?: MFAMethod[];
  password_update_recommended?: boolean;
};

function toLoginResult(body: LoginResponseBody): LoginResult {
  if (body.handoff) return { kind: "handoff", handoff: body.handoff };
  if (body.mfa_required && body.mfa_token) {
    return { kind: "mfa_required", challengeToken: body.mfa_token, methods: body.mfa_methods ?? [] };
  }
  return { kind: "authenticated", passwordUpdateRecommended: body.password_update_recommended === true };
}

// login backs POST /auth/login (auth-internals.md §3). A successful full
// login carries no user/tenant data of its own (only expires_in, per the
// documented response body) — the caller still needs fetchCurrentSession
// afterward to hydrate the authenticated state.
export async function login(credentials: LoginCredentials): Promise<LoginResult> {
  const response = await fetch("/auth/login", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      email: credentials.email,
      password: credentials.password,
      tenant: credentials.tenant,
      remember: credentials.remember ?? false,
    }),
  });
  if (!response.ok) throw await readError(response);
  return toLoginResult((await response.json()) as LoginResponseBody);
}

// exchangeHandoff backs POST /auth/handoff (auth-internals.md §3
// "Shared-domain handoff"): on the tenant's own host, it trades a handoff
// code for the session or an MFA challenge. A 401
// auth.handoff_code_invalid means the code expired or was already used.
export async function exchangeHandoff(code: string): Promise<LoginResult> {
  const response = await fetch("/auth/handoff", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!response.ok) throw await readError(response);
  return toLoginResult((await response.json()) as LoginResponseBody);
}

// submitMFACode backs POST /auth/mfa/verify (auth-internals.md §8). Same
// as login, a successful verify carries no user/tenant data of its own;
// it resolves to the password_update_recommended flag.
export async function submitMFACode(challengeToken: string, code: string, method: MFAMethod): Promise<boolean> {
  const response = await fetch("/auth/mfa/verify", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mfa_token: challengeToken, type: method, code }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json().catch(() => ({}))) as { password_update_recommended?: boolean };
  return body.password_update_recommended === true;
}

// updateProfile backs PATCH /auth/me (shell-ux.md §4.1). Its own response
// carries only the saved name, not the full CurrentUser shape (no
// resolved avatar URL, roles, etc.) — the caller re-fetches the session
// afterward, same "the mutation call itself doesn't carry the hydrated
// state" pattern login/submitMFA already use.
export async function updateProfile(input: UpdateProfileInput): Promise<void> {
  const response = await fetch("/auth/me", {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: input.name, avatar_id: input.avatarId }),
  });
  if (!response.ok) throw await readError(response);
}

// updatePreferences backs PATCH /auth/me for the Appearance page
// (shell-ux.md §4.4): only the fields given are sent.
export async function updatePreferences(input: UpdatePreferencesInput): Promise<void> {
  const body: Record<string, unknown> = {};
  if (input.theme !== undefined) body.theme = input.theme;
  if (input.locale !== undefined) body.locale = input.locale;
  if (input.timezone !== undefined) body.timezone = input.timezone;
  if (input.dateFormat !== undefined) body.date_format = input.dateFormat;
  const response = await fetch("/auth/me", {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw await readError(response);
}

// requestPasswordReset backs POST /auth/password-reset/request
// (auth-internals.md §3). The engine answers 200 for every well-formed
// request, so success says nothing about whether the email is registered.
export async function requestPasswordReset(input: PasswordResetRequest): Promise<void> {
  const response = await fetch("/auth/password-reset/request", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: input.email, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
}

// changePassword backs POST /auth/me/change-password (auth-internals.md §3
// "Password change"). The caller's session survives, so there's no session
// state to refresh afterwards.
export async function changePassword(input: ChangePasswordInput): Promise<void> {
  const response = await fetch("/auth/me/change-password", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ current_password: input.currentPassword, new_password: input.newPassword }),
  });
  if (!response.ok) throw await readError(response);
}

// confirmPasswordReset backs POST /auth/password-reset/confirm
// (auth-internals.md §3). A 404 means the token is invalid, expired, or
// already used; a 422 (auth.password_too_weak) means the tenant's policy
// rejected the password.
export async function confirmPasswordReset(input: PasswordResetConfirmation): Promise<PasswordResetOutcome> {
  const response = await fetch("/auth/password-reset/confirm", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token: input.token, new_password: input.newPassword, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { login_required?: boolean };
  return body.login_required ? "login_required" : "signed_in";
}

// verifyEmail backs POST /auth/verify-email (auth-internals.md §3 "Email
// verification confirm"). A 404 means the token is invalid, expired, or
// already used.
export async function verifyEmail(input: EmailVerification): Promise<EmailVerificationOutcome> {
  const response = await fetch("/auth/verify-email", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token: input.token, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { login_required?: boolean };
  return body.login_required ? "login_required" : "signed_in";
}

// beginTOTPEnrollment backs POST /auth/mfa/enroll/totp (auth-internals.md
// §8 "MFA enrollment"): a new secret held pending until confirmed.
export async function beginTOTPEnrollment(): Promise<TOTPEnrollment> {
  const response = await fetch("/auth/mfa/enroll/totp", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { enrollment_id: string; qr_svg: string; secret: string };
  return { enrollmentId: body.enrollment_id, qrSvg: body.qr_svg, secret: body.secret };
}

// confirmTOTPEnrollment backs POST /auth/mfa/enroll/totp/confirm. Resolves
// to the recovery codes issued with the user's first factor, or null when
// they already hold some. Rejects with invalid_mfa_code (400),
// mfa_enrollment_not_found (404), or mfa_required/mfa_reverify_required
// (403) for an enrolled user whose session needs fresh MFA. The session's
// access token is reissued by cookie; call reloadSession to pick up the
// cleared mfaSetupRequired.
export async function confirmTOTPEnrollment(input: TOTPEnrollmentConfirmation): Promise<string[] | null> {
  const response = await fetch("/auth/mfa/enroll/totp/confirm", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enrollment_id: input.enrollmentId, code: input.code, label: input.label }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { recovery_codes: string[] | null };
  return body.recovery_codes ?? null;
}

// resendVerificationEmail backs POST /auth/verify-email/resend
// (auth-internals.md §3). The engine answers 200 for every well-formed
// request, so success says nothing about whether a link was sent.
export async function resendVerificationEmail(input: VerificationEmailRequest): Promise<void> {
  const response = await fetch("/auth/verify-email/resend", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: input.email, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
}

// register backs POST /auth/register (shell-ux.md §2.2). A 409 carries
// auth.email_already_exists or tenant.slug_taken; a 422 (validation_failed)
// carries per-field messages in details.
export async function register(input: Registration): Promise<RegisterOutcome> {
  const response = await fetch("/auth/register", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: input.name,
      email: input.email,
      password: input.password,
      company_name: input.companyName,
    }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json().catch(() => ({}))) as {
    tenant_slug?: string;
    handoff?: SignInHandoff;
    login_required?: boolean;
    requires_email_verification?: boolean;
    provisioning_pending?: boolean;
  };
  const tenantSlug = body.tenant_slug ?? "";
  if (response.status === 202) {
    if (body.requires_email_verification) return { kind: "verification_required", tenantSlug };
    return { kind: "provisioning_pending", tenantSlug };
  }
  if (body.handoff) return { kind: "handoff", tenantSlug, handoff: body.handoff };
  return body.login_required ? { kind: "login_required", tenantSlug } : { kind: "signed_in", tenantSlug };
}

// checkSlug backs GET /auth/check-slug. false covers a malformed or reserved
// slug as well as a taken one.
export async function checkSlug(slug: string, signal?: AbortSignal): Promise<boolean> {
  const response = await fetch(`/auth/check-slug?${new URLSearchParams({ slug })}`, {
    credentials: "include",
    ...(signal ? { signal } : {}),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { available?: boolean };
  return body.available === true;
}

// fetchInviteInfo backs GET /auth/accept-invite/info (shell-ux.md §2.5).
// A 404 (invalid_invite) covers every dead link: unknown, expired,
// revoked, or already accepted.
export async function fetchInviteInfo(link: InviteLink): Promise<InviteInfo> {
  const query = new URLSearchParams({ token: link.token, tenant: link.tenant });
  const response = await fetch(`/auth/accept-invite/info?${query}`, { credentials: "include" });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as {
    tenant_name: string;
    email: string;
    name: string | null;
    password_required: boolean;
  };
  return {
    tenantName: body.tenant_name,
    email: body.email,
    name: body.name,
    passwordRequired: body.password_required,
  };
}

// acceptInvite backs POST /auth/accept-invite (shell-ux.md §2.5).
export async function acceptInvite(input: InviteAcceptance): Promise<InviteAcceptOutcome> {
  const response = await fetch("/auth/accept-invite", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token: input.token, tenant: input.tenant, password: input.password }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { login_required?: boolean };
  return body.login_required ? "login_required" : "signed_in";
}

// logout backs POST /auth/logout (auth-internals.md §4). Deliberately
// swallows the response — the machine transitions to unauthenticated
// regardless of the call's outcome (a session the user asked to end is
// never left looking authenticated).
export async function logout(): Promise<void> {
  try {
    await fetch("/auth/logout", { method: "POST", credentials: "include" });
  } catch {
    // Network failure — the client-side session still ends below.
  }
}
