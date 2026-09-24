import { AppError } from "../error/app-error.js";
import type {
  ChangePasswordInput,
  CurrentTenant,
  CurrentUser,
  InviteAcceptance,
  InviteAcceptOutcome,
  InviteInfo,
  InviteLink,
  LoginCredentials,
  MFAMethod,
  PasswordResetConfirmation,
  PasswordResetOutcome,
  PasswordResetRequest,
  TenantContext,
  UpdateProfileInput,
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
  };
  tenant: {
    id: string;
    slug: string;
    name: string;
    plan: string;
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
  };
}

function mapTenant(tenant: MeResponseBody["tenant"]): CurrentTenant {
  return { id: tenant.id, slug: tenant.slug, name: tenant.name, plan: tenant.plan };
}

// A 429's Retry-After header (whole seconds) surfaces as
// details.retryAfter, so a caller can show a lockout countdown.
async function readError(response: Response): Promise<AppError> {
  let code = "unknown_error";
  let message = response.statusText || "request failed";
  try {
    const body = (await response.json()) as { error?: { code?: string; message?: string } };
    if (body.error?.code) code = body.error.code;
    if (body.error?.message) message = body.error.message;
  } catch {
    // Non-JSON or empty body — fall back to the status text above.
  }
  const retryAfter = Number.parseInt(response.headers.get("Retry-After") ?? "", 10);
  const details = Number.isFinite(retryAfter) ? { retryAfter } : null;
  return new AppError({ code, message, httpStatus: response.status, details });
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
  if (!response.ok) return null;
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
    if (!response.ok) return null;
    const body = (await response.json()) as {
      tenant: { slug: string; name: string } | null;
      registration_enabled: boolean;
    };
    return {
      tenant: body.tenant ? { slug: body.tenant.slug, name: body.tenant.name } : null,
      registrationEnabled: body.registration_enabled === true,
    };
  } catch {
    return null;
  }
}

export type LoginResult =
  | { kind: "authenticated"; passwordUpdateRecommended: boolean }
  | { kind: "mfa_required"; challengeToken: string; methods: MFAMethod[] };

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

  const body = (await response.json()) as {
    mfa_required?: boolean;
    mfa_token?: string;
    mfa_methods?: MFAMethod[];
    password_update_recommended?: boolean;
  };
  if (body.mfa_required && body.mfa_token) {
    return { kind: "mfa_required", challengeToken: body.mfa_token, methods: body.mfa_methods ?? [] };
  }
  return { kind: "authenticated", passwordUpdateRecommended: body.password_update_recommended === true };
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
