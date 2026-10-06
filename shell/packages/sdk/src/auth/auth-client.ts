import { AppError } from "../error/app-error.js";
import type { ContrastPreference, ThemePreference } from "../react/use-theme.js";
import { noteTenantSuspension } from "./tenant-suspension.js";
import type {
  ActiveSession,
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
  MFACodeConfirmation,
  MFAFactors,
  MFAMethod,
  MFAVerification,
  PasskeyEnrollmentConfirmation,
  PasswordResetConfirmation,
  PasswordResetOutcome,
  PasswordResetRequest,
  RegisterOutcome,
  Registration,
  SignInHandoff,
  TenantContext,
  TenantSelection,
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
    password_change_required: boolean;
    password_min_length: number;
    phone: string | null;
    title: string | null;
    theme: ThemePreference;
    contrast: ContrastPreference;
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
    password_min_length?: number;
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
    passwordChangeRequired: user.password_change_required === true,
    passwordMinLength: minLengthOr(user.password_min_length),
    phone: user.phone ?? null,
    title: user.title ?? null,
    theme: user.theme,
    contrast: user.contrast,
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
    passwordMinLength: minLengthOr(tenant.password_min_length),
  };
}

// The global minimum password length (auth-internals.md §3 "Password
// strength validation"): the fallback when a response carries none.
export const GLOBAL_PASSWORD_MIN_LENGTH = 12;

function minLengthOr(value: unknown): number {
  return typeof value === "number" && value > 0 ? value : GLOBAL_PASSWORD_MIN_LENGTH;
}

export function passwordMinLengthFrom(err: unknown): number | null {
  if (!(err instanceof AppError) || err.code !== "auth.password_too_weak") return null;
  const value = err.details?.min_length;
  return typeof value === "number" && value > 0 ? value : null;
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
  } catch {}
  const retryAfter = Number.parseInt(response.headers.get("Retry-After") ?? "", 10);
  const details =
    bodyDetails || Number.isFinite(retryAfter)
      ? { ...bodyDetails, ...(Number.isFinite(retryAfter) ? { retryAfter } : {}) }
      : null;
  const error = new AppError({ code, message, httpStatus: response.status, details });
  noteTenantSuspension(error);
  return error;
}

// Session-check failures resolve to no session because the auth machine has no separate check-error state.
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

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value !== "" ? value : null;
}

// Lookup failures fall back to the company-slug form; a missing workspace keeps its distinct error screen.
export async function fetchTenantContext(): Promise<TenantContext | null> {
  try {
    const response = await fetch("/auth/tenant-context", { credentials: "include" });
    if (!response.ok) {
      if (response.status !== 403 && response.status !== 404) return null;
      const error = await readError(response);
      if (error.httpStatus !== 404 || error.code !== "tenant_not_found") return null;
      return {
        tenant: null,
        registrationEnabled: false,
        termsUrl: null,
        appUrl: nonEmptyString(error.details?.app_url),
        workspaceNotFound: true,
        passwordMinLength: GLOBAL_PASSWORD_MIN_LENGTH,
      };
    }
    const body = (await response.json()) as {
      tenant: { slug: string; name: string } | null;
      registration_enabled: boolean;
      terms_url?: string | null;
      app_url?: string;
      password_min_length?: number;
    };
    return {
      tenant: body.tenant ? { slug: body.tenant.slug, name: body.tenant.name } : null,
      registrationEnabled: body.registration_enabled === true,
      termsUrl: nonEmptyString(body.terms_url),
      appUrl: nonEmptyString(body.app_url),
      workspaceNotFound: false,
      passwordMinLength: minLengthOr(body.password_min_length),
    };
  } catch {
    return null;
  }
}

export type LoginResult =
  | { kind: "authenticated"; passwordUpdateRecommended: boolean; passwordUpdateDeadline: string | null }
  | { kind: "mfa_required"; challengeToken: string; methods: MFAMethod[] }
  | { kind: "handoff"; handoff: SignInHandoff };

type LoginResponseBody = {
  handoff?: SignInHandoff;
  mfa_required?: boolean;
  mfa_token?: string;
  mfa_methods?: MFAMethod[];
  password_update_recommended?: boolean;
  password_update_deadline?: string;
};

function toLoginResult(body: LoginResponseBody): LoginResult {
  if (body.handoff) return { kind: "handoff", handoff: body.handoff };
  if (body.mfa_required && body.mfa_token) {
    return { kind: "mfa_required", challengeToken: body.mfa_token, methods: body.mfa_methods ?? [] };
  }
  return {
    kind: "authenticated",
    passwordUpdateRecommended: body.password_update_recommended === true,
    passwordUpdateDeadline: body.password_update_deadline ?? null,
  };
}

// The login response contains no user or tenant data; a session reload supplies the authenticated identity.
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

export function tenantSelectionFrom(err: unknown): TenantSelection | null {
  if (!(err instanceof AppError) || err.httpStatus !== 409 || err.code !== "tenant_required") return null;
  const token = err.details?.selection_token;
  const raw = err.details?.tenants;
  if (typeof token !== "string" || token === "" || !Array.isArray(raw)) return null;
  const tenants = raw.flatMap((entry: unknown) => {
    if (entry === null || typeof entry !== "object") return [];
    const { slug, name } = entry as { slug?: unknown; name?: unknown };
    return typeof slug === "string" && typeof name === "string" ? [{ slug, name }] : [];
  });
  return { tenants, selectionToken: token };
}

export async function selectTenant(selectionToken: string, tenant: string): Promise<LoginResult> {
  const response = await fetch("/auth/select-tenant", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ selection_token: selectionToken, tenant }),
  });
  if (!response.ok) throw await readError(response);
  return toLoginResult((await response.json()) as LoginResponseBody);
}

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

function verificationBody(input: MFAVerification) {
  return input.type === "webauthn"
    ? { type: input.type, ceremony_id: input.ceremonyId, response: input.response }
    : { type: input.type, code: input.code };
}

export async function verifyMFA(
  challengeToken: string,
  input: MFAVerification,
): Promise<{ recommended: boolean; deadline: string | null }> {
  const response = await fetch("/auth/mfa/verify", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mfa_token: challengeToken, ...verificationBody(input) }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json().catch(() => ({}))) as LoginResponseBody;
  return { recommended: body.password_update_recommended === true, deadline: body.password_update_deadline ?? null };
}

export async function updateProfile(input: UpdateProfileInput): Promise<void> {
  const response = await fetch("/auth/me", {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: input.name, avatar_id: input.avatarId, phone: input.phone, title: input.title }),
  });
  if (!response.ok) throw await readError(response);
}

export async function updatePreferences(input: UpdatePreferencesInput): Promise<void> {
  const body: Record<string, unknown> = {};
  if (input.theme !== undefined) body.theme = input.theme;
  if (input.contrast !== undefined) body.contrast = input.contrast;
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

// The API returns success for every well-formed request to prevent account enumeration.
export async function requestPasswordReset(input: PasswordResetRequest): Promise<void> {
  const response = await fetch("/auth/password-reset/request", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: input.email, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
}

export async function changePassword(input: ChangePasswordInput): Promise<void> {
  const response = await fetch("/auth/me/change-password", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ current_password: input.currentPassword, new_password: input.newPassword }),
  });
  if (!response.ok) throw await readError(response);
}

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

// Confirmation reissues the access-token cookie; reloadSession reads the cleared MFA setup flag.
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

// Re-verification reissues the access-token cookie without creating a new session.
export async function reverifyMFA(input: MFAVerification): Promise<void> {
  const response = await fetch("/auth/mfa/reverify", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(verificationBody(input)),
  });
  if (!response.ok) throw await readError(response);
}

export async function confirmPasskeyEnrollment(input: PasskeyEnrollmentConfirmation): Promise<string[] | null> {
  const response = await fetch("/auth/mfa/enroll/webauthn/confirm", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ceremony_id: input.ceremonyId, response: input.response, label: input.label }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { recovery_codes: string[] | null };
  return body.recovery_codes ?? null;
}

export async function fetchPasskeyOptions<T>(path: string, body: object, signal?: AbortSignal) {
  const response = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    ...(signal ? { signal } : {}),
  });
  if (!response.ok) throw await readError(response);
  return (await response.json()) as { ceremony_id: string; options: { publicKey: T } };
}

interface MFAFactorsWire {
  factors: {
    tenant_only: boolean;
    id: string;
    type: "totp" | "webauthn";
    label: string | null;
    created_at: string;
    last_used_at: string | null;
  }[];
  recovery_codes_remaining: number;
  required_by_policy: boolean;
}

export async function fetchMFAFactors(signal?: AbortSignal): Promise<MFAFactors> {
  const response = await fetch("/auth/mfa/factors", { credentials: "include", ...(signal ? { signal } : {}) });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as MFAFactorsWire;
  return {
    factors: body.factors.map((f) => ({
      tenantOnly: f.tenant_only,
      id: f.id,
      type: f.type,
      label: f.label,
      createdAt: f.created_at,
      lastUsedAt: f.last_used_at,
    })),
    recoveryCodesRemaining: body.recovery_codes_remaining,
    requiredByPolicy: body.required_by_policy,
  };
}

// Removing a platform factor revokes sessions in every tenant; a tenant
// factor revokes sessions in its tenant. Rejects with
// mfa_factor_not_found (404), invalid_mfa_code (401),
// mfa_required_by_policy (409), or mfa_locked (423).
export async function removeMFAFactor(id: string, confirmation: MFACodeConfirmation): Promise<void> {
  const response = await fetch(`/auth/mfa/factors/${encodeURIComponent(id)}/remove`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type: confirmation.type, code: confirmation.code }),
  });
  if (!response.ok) throw await readError(response);
}

// A wrong admin password answers 401 invalid_password; raw fetch keeps it out of
// the global session-expiry handler.
export async function resetUserMFA(userId: string, password: string): Promise<void> {
  const response = await fetch(`/admin/users/${encodeURIComponent(userId)}/mfa/reset`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  });
  if (!response.ok) throw await readError(response);
}

// Regenerating codes signs out the account's other sessions in this tenant.
export async function regenerateRecoveryCodes(confirmation: MFACodeConfirmation): Promise<string[]> {
  const response = await fetch("/auth/mfa/recovery-codes/regenerate", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type: confirmation.type, code: confirmation.code }),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { recovery_codes: string[] };
  return body.recovery_codes;
}

interface SessionWire {
  id: string;
  user_agent: string | null;
  ip_address: string | null;
  country_code: string | null;
  signed_in_at: string;
  last_active_at: string;
  persistent: boolean;
  current: boolean;
}

export async function fetchSessions(signal?: AbortSignal): Promise<ActiveSession[]> {
  const response = await fetch("/auth/sessions", { credentials: "include", ...(signal ? { signal } : {}) });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { sessions: SessionWire[] };
  return body.sessions.map((s) => ({
    id: s.id,
    userAgent: s.user_agent,
    ipAddress: s.ip_address,
    countryCode: s.country_code,
    signedInAt: s.signed_in_at,
    lastActiveAt: s.last_active_at,
    persistent: s.persistent,
    current: s.current,
  }));
}

export async function revokeSession(id: string): Promise<void> {
  const response = await fetch(`/auth/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!response.ok) throw await readError(response);
}

export async function revokeOtherSessions(): Promise<number> {
  const response = await fetch("/auth/sessions", { method: "DELETE", credentials: "include" });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { revoked: number };
  return body.revoked;
}

// The API returns success even when no link is sent to prevent account enumeration.
export async function resendVerificationEmail(input: VerificationEmailRequest): Promise<void> {
  const response = await fetch("/auth/verify-email/resend", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: input.email, tenant: input.tenant }),
  });
  if (!response.ok) throw await readError(response);
}

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

export async function checkSlug(slug: string, signal?: AbortSignal): Promise<boolean> {
  const response = await fetch(`/auth/check-slug?${new URLSearchParams({ slug })}`, {
    credentials: "include",
    ...(signal ? { signal } : {}),
  });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as { available?: boolean };
  return body.available === true;
}

export async function fetchInviteInfo(link: InviteLink): Promise<InviteInfo> {
  const query = new URLSearchParams({ token: link.token, tenant: link.tenant });
  const response = await fetch(`/auth/accept-invite/info?${query}`, { credentials: "include" });
  if (!response.ok) throw await readError(response);
  const body = (await response.json()) as {
    tenant_name: string;
    email: string;
    name: string | null;
    password_required: boolean;
    password_min_length?: number;
  };
  return {
    tenantName: body.tenant_name,
    email: body.email,
    name: body.name,
    passwordRequired: body.password_required,
    passwordMinLength: minLengthOr(body.password_min_length),
  };
}

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

// Client auth state ends even when the server cannot process logout.
export async function logout(): Promise<void> {
  try {
    await fetch("/auth/logout", { method: "POST", credentials: "include" });
  } catch {}
}
