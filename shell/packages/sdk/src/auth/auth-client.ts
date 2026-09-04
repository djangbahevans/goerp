import { AppError } from "../error/app-error.js";
import type { CurrentTenant, CurrentUser, LoginCredentials, MFAMethod } from "./types.js";

interface MeResponseBody {
  user: {
    id: string;
    email: string;
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
    roles: user.roles,
    amr: user.amr,
    mfaVerifiedAt: user.mfa_verified_at,
  };
}

function mapTenant(tenant: MeResponseBody["tenant"]): CurrentTenant {
  return { id: tenant.id, slug: tenant.slug, name: tenant.name, plan: tenant.plan };
}

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
  return new AppError({ code, message, httpStatus: response.status });
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

export type LoginResult =
  | { kind: "authenticated" }
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
    body: JSON.stringify(credentials),
  });
  if (!response.ok) throw await readError(response);

  const body = (await response.json()) as {
    mfa_required?: boolean;
    mfa_token?: string;
    mfa_methods?: MFAMethod[];
  };
  if (body.mfa_required && body.mfa_token) {
    return { kind: "mfa_required", challengeToken: body.mfa_token, methods: body.mfa_methods ?? [] };
  }
  return { kind: "authenticated" };
}

// submitMFACode backs POST /auth/mfa/verify (auth-internals.md §8). Same
// as login, a successful verify carries no user/tenant data of its own.
export async function submitMFACode(challengeToken: string, code: string, method: MFAMethod): Promise<void> {
  const response = await fetch("/auth/mfa/verify", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mfa_token: challengeToken, type: method, code }),
  });
  if (!response.ok) throw await readError(response);
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
