import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import {
  acceptInvite,
  beginTOTPEnrollment,
  changePassword,
  checkSlug,
  confirmPasswordReset,
  confirmTOTPEnrollment,
  exchangeHandoff,
  fetchCurrentSession,
  fetchInviteInfo,
  fetchMFAFactors,
  fetchSessions,
  fetchTenantContext,
  login,
  logout,
  passwordMinLengthFrom,
  regenerateRecoveryCodes,
  register,
  removeMFAFactor,
  requestPasswordReset,
  resendVerificationEmail,
  reverifyMFA,
  revokeOtherSessions,
  revokeSession,
  selectTenant,
  tenantSelectionFrom,
  updatePreferences,
  updateProfile,
  verifyEmail,
  verifyMFA,
} from "./auth-client.js";
import { tenantSuspension } from "./tenant-suspension.js";

function jsonResponse(status: number, body: unknown, statusText = "", headers: Record<string, string> = {}): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText,
    headers: new Headers(headers),
    json: async () => body,
  } as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  tenantSuspension.set(false);
});

describe("fetchCurrentSession", () => {
  it("maps a 200 response to camelCase user/tenant", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(200, {
        user: {
          id: "u1",
          email: "a@example.com",
          contact_id: "c1",
          name: "Ada Lovelace",
          avatar_url: null,
          roles: ["admin"],
          amr: ["pwd"],
          mfa_verified_at: null,
          password_change_required: true,
          password_min_length: 18,
          phone: "+233201234567",
          title: "Consultant",
          theme: "dark",
          contrast: "high",
          locale: "fr",
          timezone: null,
          date_format: "iso",
        },
        tenant: {
          id: "t1",
          slug: "acme",
          name: "Acme",
          plan: "pro",
          default_locale: "en",
          default_timezone: "UTC",
          available_locales: ["en", "fr"],
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const session = await fetchCurrentSession();

    expect(session).toEqual({
      user: {
        id: "u1",
        email: "a@example.com",
        name: "Ada Lovelace",
        contactId: "c1",
        avatarUrl: null,
        roles: ["admin"],
        amr: ["pwd"],
        mfaVerifiedAt: null,
        mfaSetupRequired: false,
        passwordChangeRequired: true,
        passwordMinLength: 18,
        phone: "+233201234567",
        title: "Consultant",
        theme: "dark",
        contrast: "high",
        locale: "fr",
        timezone: null,
        dateFormat: "iso",
      },
      tenant: {
        id: "t1",
        slug: "acme",
        name: "Acme",
        plan: "pro",
        defaultLocale: "en",
        defaultTimezone: "UTC",
        availableLocales: ["en", "fr"],
        passwordMinLength: 12,
      },
    });
    expect(fetchMock).toHaveBeenCalledWith("/auth/me", { credentials: "include" });
  });

  it("returns null on 401", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(401, { error: { code: "unauthenticated" } })),
    );
    expect(await fetchCurrentSession()).toBeNull();
  });

  it("returns null on any other non-2xx status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(500, {})),
    );
    expect(await fetchCurrentSession()).toBeNull();
  });

  it("returns null and flags the tenant as suspended on a 403 tenant_suspended", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: { code: "tenant_suspended", message: "tenant suspended" } })),
    );
    expect(await fetchCurrentSession()).toBeNull();
    expect(tenantSuspension.get()).toBe(true);
  });

  it("returns null when the request itself throws", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("network down");
      }),
    );
    expect(await fetchCurrentSession()).toBeNull();
  });

  it("returns null when a 200 response isn't valid JSON (e.g. a dev-server SPA fallback)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => {
          throw new SyntaxError("Unexpected token '<'");
        },
      })) as unknown as typeof fetch,
    );
    expect(await fetchCurrentSession()).toBeNull();
  });
});

describe("TOTP enrollment", () => {
  it("maps /auth/me's mfa_setup_required onto the user", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(200, {
          user: {
            id: "u1",
            email: "a@example.com",
            contact_id: null,
            name: null,
            avatar_url: null,
            roles: [],
            amr: ["pwd"],
            mfa_verified_at: null,
            mfa_setup_required: true,
          },
          tenant: {
            id: "t1",
            slug: "acme",
            name: "Acme",
            plan: "pro",
            defaultLocale: "en",
            defaultTimezone: "UTC",
            availableLocales: ["en"],
            passwordMinLength: 12,
          },
        }),
      ),
    );
    expect((await fetchCurrentSession())?.user.mfaSetupRequired).toBe(true);
  });

  it("beginTOTPEnrollment maps the pending enrollment", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(200, { enrollment_id: "e1", qr_svg: "<svg/>", secret: "JBSWY3DPEHPK3PXP" }),
    );
    vi.stubGlobal("fetch", fetchMock);

    expect(await beginTOTPEnrollment()).toEqual({ enrollmentId: "e1", qrSvg: "<svg/>", secret: "JBSWY3DPEHPK3PXP" });
    expect(fetchMock).toHaveBeenCalledWith("/auth/mfa/enroll/totp", expect.objectContaining({ method: "POST" }));
  });

  it("confirmTOTPEnrollment returns the codes, or null when none were issued", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { recovery_codes: ["ABCDE-FGHIJ"], expires_in: 900 }))
      .mockResolvedValueOnce(jsonResponse(200, { recovery_codes: null, expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await confirmTOTPEnrollment({ enrollmentId: "e1", code: "123456" })).toEqual(["ABCDE-FGHIJ"]);
    expect(await confirmTOTPEnrollment({ enrollmentId: "e2", code: "654321" })).toBeNull();
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string)).toEqual({ enrollment_id: "e1", code: "123456" });
  });

  it("confirmTOTPEnrollment rejects with the server's error code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(400, { error: { code: "invalid_mfa_code", message: "invalid MFA code" } })),
    );
    await expect(confirmTOTPEnrollment({ enrollmentId: "e1", code: "000000" })).rejects.toMatchObject({
      code: "invalid_mfa_code",
      httpStatus: 400,
    });
  });
});

describe("MFA factor management", () => {
  it("fetchMFAFactors maps the wire shape", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(200, {
          factors: [
            {
              id: "f1",
              type: "totp",
              tenant_only: false,
              label: "iPhone",
              created_at: "2026-08-01T10:00:00Z",
              last_used_at: null,
            },
            {
              id: "f2",
              type: "totp",
              tenant_only: true,
              label: "Work phone",
              created_at: "2026-08-02T10:00:00Z",
              last_used_at: null,
            },
          ],
          recovery_codes_remaining: 7,
          required_by_policy: true,
        }),
      ),
    );

    expect(await fetchMFAFactors()).toEqual({
      factors: [
        {
          id: "f1",
          type: "totp",
          tenantOnly: false,
          label: "iPhone",
          createdAt: "2026-08-01T10:00:00Z",
          lastUsedAt: null,
        },
        {
          id: "f2",
          type: "totp",
          tenantOnly: true,
          label: "Work phone",
          createdAt: "2026-08-02T10:00:00Z",
          lastUsedAt: null,
        },
      ],
      recoveryCodesRemaining: 7,
      requiredByPolicy: true,
    });
  });

  it("removeMFAFactor posts the code to the factor's remove route", async () => {
    const fetchMock = vi.fn(async () => ({ ok: true, status: 204 }) as Response);
    vi.stubGlobal("fetch", fetchMock);

    await removeMFAFactor("f/1", { type: "recovery_code", code: "ABCDE-FGHIJ" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/mfa/factors/f%2F1/remove",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ type: "recovery_code", code: "ABCDE-FGHIJ" }) }),
    );
  });

  it("removeMFAFactor rejects with the server's error code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(409, { error: { code: "mfa_required_by_policy", message: "required" } })),
    );
    await expect(removeMFAFactor("f1", { type: "totp", code: "123456" })).rejects.toMatchObject({
      code: "mfa_required_by_policy",
      httpStatus: 409,
    });
  });

  it("regenerateRecoveryCodes resolves to the new codes", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { recovery_codes: ["ABCDE-FGHIJ"] }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await regenerateRecoveryCodes({ type: "totp", code: "123456" })).toEqual(["ABCDE-FGHIJ"]);
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/mfa/recovery-codes/regenerate",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("reverifyMFA rejects an incorrect code with invalid_mfa_code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(401, { error: { code: "invalid_mfa_code", message: "invalid MFA code" } })),
    );
    await expect(reverifyMFA({ type: "totp", code: "000000" })).rejects.toMatchObject({ code: "invalid_mfa_code" });
  });
});

describe("login", () => {
  const credentials = { email: "a@example.com", password: "hunter2", tenant: "acme" };

  it("returns authenticated for a full-session response", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await login(credentials);

    expect(result).toEqual({ kind: "authenticated", passwordUpdateRecommended: false, passwordUpdateDeadline: null });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/login",
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
  });

  it("carries password_update_recommended on a full-session response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { expires_in: 900, password_update_recommended: true })),
    );

    expect(await login(credentials)).toEqual({
      kind: "authenticated",
      passwordUpdateRecommended: true,
      passwordUpdateDeadline: null,
    });
  });

  it("returns mfa_required with the challenge token and methods", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(200, { mfa_required: true, mfa_token: "mfa-tok", mfa_methods: ["totp", "webauthn"] }),
      ),
    );

    const result = await login(credentials);

    expect(result).toEqual({ kind: "mfa_required", challengeToken: "mfa-tok", methods: ["totp", "webauthn"] });
  });

  it("returns the handoff a shared-domain sign-in answers with", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { handoff: { host: "acme.localhost", code: "c0de" } })),
    );

    expect(await login(credentials)).toEqual({ kind: "handoff", handoff: { host: "acme.localhost", code: "c0de" } });
  });

  it("throws an AppError built from the error response on failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(401, { error: { code: "invalid_credentials", message: "invalid email or password" } }),
      ),
    );

    await expect(login(credentials)).rejects.toMatchObject({
      code: "invalid_credentials",
      httpStatus: 401,
      message: "invalid email or password",
    });
  });

  it("sends remember, defaulting it to false", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await login(credentials);
    await login({ ...credentials, remember: true });

    expect(JSON.parse(fetchMock.mock.calls[0]?.[1].body as string)).toEqual({ ...credentials, remember: false });
    expect(JSON.parse(fetchMock.mock.calls[1]?.[1].body as string)).toEqual({ ...credentials, remember: true });
  });

  it("surfaces a 429's Retry-After as details.retryAfter", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(429, { error: { code: "rate_limit_exceeded", message: "too many requests" } }, "", {
          "Retry-After": "42",
        }),
      ),
    );

    await expect(login(credentials)).rejects.toMatchObject({
      code: "rate_limit_exceeded",
      httpStatus: 429,
      details: { retryAfter: 42 },
    });
  });

  it("rejects with an AppError even when the error body isn't JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        headers: new Headers(),
        json: async () => {
          throw new Error("not json");
        },
      })) as unknown as typeof fetch,
    );

    const err = await login(credentials).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AppError);
    expect((err as AppError).httpStatus).toBe(500);
  });
});

describe("fetchTenantContext", () => {
  it("maps a resolved tenant", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(200, {
          tenant: { slug: "acme", name: "Acme Corp" },
          registration_enabled: true,
          terms_url: "https://example.com/terms",
          app_url: "https://app.goerp.io",
          password_min_length: 14,
        }),
      ),
    );

    expect(await fetchTenantContext()).toEqual({
      tenant: { slug: "acme", name: "Acme Corp" },
      registrationEnabled: true,
      termsUrl: "https://example.com/terms",
      appUrl: "https://app.goerp.io",
      workspaceNotFound: false,
      passwordMinLength: 14,
    });
  });

  it("maps a shared-domain response's null tenant", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { tenant: null, registration_enabled: false })),
    );

    expect(await fetchTenantContext()).toEqual({
      tenant: null,
      registrationEnabled: false,
      termsUrl: null,
      appUrl: null,
      workspaceNotFound: false,
      passwordMinLength: 12,
    });
  });

  it("maps a 404 tenant_not_found to a workspace-not-found context carrying the app URL", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(404, {
          error: { code: "tenant_not_found", message: "no workspace", details: { app_url: "https://app.goerp.io" } },
        }),
      ),
    );

    expect(await fetchTenantContext()).toEqual({
      tenant: null,
      registrationEnabled: false,
      termsUrl: null,
      appUrl: "https://app.goerp.io",
      workspaceNotFound: true,
      passwordMinLength: 12,
    });
  });

  it("resolves to null on a non-200 or network failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(404, { error: { code: "not_found" } })),
    );
    expect(await fetchTenantContext()).toBeNull();

    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("network");
      }),
    );
    expect(await fetchTenantContext()).toBeNull();
  });
});

describe("tenant suspension", () => {
  it("flags a 403 tenant_suspended tenant-context lookup and resolves to null", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: { code: "tenant_suspended", message: "tenant suspended" } })),
    );
    expect(await fetchTenantContext()).toBeNull();
    expect(tenantSuspension.get()).toBe(true);
  });

  it("flags a 403 tenant_suspended from any other auth call", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: { code: "tenant_suspended", message: "tenant suspended" } })),
    );
    await expect(login({ email: "ada@example.com", password: "pw", tenant: "acme" })).rejects.toMatchObject({
      httpStatus: 403,
      code: "tenant_suspended",
    });
    expect(tenantSuspension.get()).toBe(true);
  });

  it("leaves the flag alone for other 403s", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: { code: "tenant_offboarding" } })),
    );
    expect(await fetchTenantContext()).toBeNull();
    expect(tenantSuspension.get()).toBe(false);
  });
});

describe("verifyMFA", () => {
  it("posts the challenge token, type, and code", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await verifyMFA("mfa-tok", { type: "totp", code: "123456" })).toEqual({
      recommended: false,
      deadline: null,
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/mfa/verify",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ mfa_token: "mfa-tok", type: "totp", code: "123456" }),
      }),
    );
  });

  it("resolves to password_update_recommended", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { expires_in: 900, password_update_recommended: true })),
    );

    expect(await verifyMFA("mfa-tok", { type: "totp", code: "123456" })).toEqual({ recommended: true, deadline: null });
  });

  it("throws an AppError on an invalid code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(401, { error: { code: "invalid_mfa_code", message: "invalid MFA code" } })),
    );

    await expect(verifyMFA("mfa-tok", { type: "totp", code: "000000" })).rejects.toMatchObject({
      code: "invalid_mfa_code",
    });
  });
});

describe("updatePreferences", () => {
  it("PATCHes only the fields given, in snake_case, with null kept as a reset", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(204, undefined));
    vi.stubGlobal("fetch", fetchMock);

    await updatePreferences({ theme: "dark", contrast: "high", locale: null, dateFormat: "iso" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/me",
      expect.objectContaining({
        method: "PATCH",
        credentials: "include",
        body: JSON.stringify({ theme: "dark", contrast: "high", locale: null, date_format: "iso" }),
      }),
    );
  });

  it("rejects with invalid_preference and the field from a 422", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(422, {
          error: { code: "invalid_preference", message: "bad locale", details: { field: "locale" } },
        }),
      ),
    );

    const error = await updatePreferences({ locale: "xx" }).catch((err: unknown) => err);

    expect(error).toBeInstanceOf(AppError);
    expect((error as AppError).code).toBe("invalid_preference");
    expect((error as AppError).details).toEqual({ field: "locale" });
  });
});

describe("updateProfile", () => {
  it("PATCHes name and avatar_id", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { name: "Ada Lovelace" }));
    vi.stubGlobal("fetch", fetchMock);

    await updateProfile({ name: "Ada Lovelace", avatarId: "file-1" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/me",
      expect.objectContaining({
        method: "PATCH",
        credentials: "include",
        body: JSON.stringify({ name: "Ada Lovelace", avatar_id: "file-1" }),
      }),
    );
  });

  it("omits avatar_id from the body when not given", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { name: "Ada Lovelace" }));
    vi.stubGlobal("fetch", fetchMock);

    await updateProfile({ name: "Ada Lovelace" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/me",
      expect.objectContaining({ body: JSON.stringify({ name: "Ada Lovelace" }) }),
    );
  });

  it("sends an empty-string avatar_id as a real value, distinct from omitting it", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { name: "Ada Lovelace" }));
    vi.stubGlobal("fetch", fetchMock);

    await updateProfile({ name: "Ada Lovelace", avatarId: "" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/me",
      expect.objectContaining({ body: JSON.stringify({ name: "Ada Lovelace", avatar_id: "" }) }),
    );
  });

  it("throws an AppError on failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(400, { error: { code: "invalid_request", message: '"name" is required' } })),
    );

    await expect(updateProfile({ name: "" })).rejects.toMatchObject({ code: "invalid_request" });
  });
});

describe("requestPasswordReset", () => {
  it("posts the email and tenant", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);

    await requestPasswordReset({ email: "ada@example.com", tenant: "acme" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/password-reset/request",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ email: "ada@example.com", tenant: "acme" }),
      }),
    );
  });

  it("throws an AppError carrying Retry-After on a 429", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(429, { error: { code: "rate_limited", message: "slow down" } }, "", { "Retry-After": "30" }),
      ),
    );

    const err = await requestPasswordReset({ email: "ada@example.com", tenant: "acme" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AppError);
    expect((err as AppError).isRateLimited()).toBe(true);
    expect((err as AppError).details).toEqual({ retryAfter: 30 });
  });
});

describe("changePassword", () => {
  it("POSTs current_password and new_password", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);

    await changePassword({ currentPassword: "old passphrase", newPassword: "new passphrase" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/me/change-password",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ current_password: "old passphrase", new_password: "new passphrase" }),
      }),
    );
  });

  it("throws an AppError carrying the server's code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(422, { error: { code: "auth.password_too_weak", message: "password is too common" } }),
      ),
    );

    await expect(changePassword({ currentPassword: "a", newPassword: "b" })).rejects.toMatchObject({
      code: "auth.password_too_weak",
      message: "password is too common",
    });
  });
});

describe("confirmPasswordReset", () => {
  const input = { token: "raw-token", newPassword: "correct horse battery", tenant: "acme" };

  it("posts token, new_password, and tenant, resolving signed_in for a session response", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(confirmPasswordReset(input)).resolves.toBe("signed_in");
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/password-reset/confirm",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ token: "raw-token", new_password: "correct horse battery", tenant: "acme" }),
      }),
    );
  });

  it("resolves login_required when no session was issued", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { login_required: true })),
    );
    await expect(confirmPasswordReset(input)).resolves.toBe("login_required");
  });

  it("throws an AppError with the server's code on failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(404, { error: { code: "invalid_token", message: "reset link is invalid" } })),
    );
    await expect(confirmPasswordReset(input)).rejects.toMatchObject({ code: "invalid_token", httpStatus: 404 });
  });
});

describe("verifyEmail", () => {
  const input = { token: "raw-token", tenant: "acme" };

  it("posts token and tenant, resolving signed_in for a session response", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(verifyEmail(input)).resolves.toBe("signed_in");
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/verify-email",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ token: "raw-token", tenant: "acme" }),
      }),
    );
  });

  it("resolves login_required when no session was issued", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { login_required: true })),
    );
    await expect(verifyEmail(input)).resolves.toBe("login_required");
  });

  it("throws an AppError with the server's code on failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(404, { error: { code: "invalid_token", message: "link is invalid" } })),
    );
    await expect(verifyEmail(input)).rejects.toMatchObject({ code: "invalid_token", httpStatus: 404 });
  });
});

describe("resendVerificationEmail", () => {
  it("posts email and tenant", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);

    await resendVerificationEmail({ email: "ada@example.com", tenant: "acme" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/verify-email/resend",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ email: "ada@example.com", tenant: "acme" }),
      }),
    );
  });

  it("throws an AppError on failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(400, { error: { code: "invalid_request", message: "malformed request body" } })),
    );
    await expect(resendVerificationEmail({ email: "a@b.co", tenant: "acme" })).rejects.toMatchObject({
      code: "invalid_request",
      httpStatus: 400,
    });
  });
});

describe("exchangeHandoff", () => {
  it("posts the code and returns the session or MFA result", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900, password_update_recommended: true }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await exchangeHandoff("c0de")).toEqual({
      kind: "authenticated",
      passwordUpdateRecommended: true,
      passwordUpdateDeadline: null,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/handoff",
      expect.objectContaining({ method: "POST", credentials: "include", body: JSON.stringify({ code: "c0de" }) }),
    );

    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { mfa_required: true, mfa_token: "tok", mfa_methods: ["totp"] })),
    );
    expect(await exchangeHandoff("c0de")).toEqual({ kind: "mfa_required", challengeToken: "tok", methods: ["totp"] });
  });

  it("rejects a spent code with its AppError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(401, { error: { code: "auth.handoff_code_invalid", message: "sign-in handoff expired" } }),
      ),
    );

    await expect(exchangeHandoff("spent")).rejects.toMatchObject({ code: "auth.handoff_code_invalid" });
  });
});

describe("passwordMinLengthFrom", () => {
  it("reads details.min_length off a password_too_weak rejection only", () => {
    const tooWeak = new AppError({
      code: "auth.password_too_weak",
      message: "password is too short",
      httpStatus: 422,
      details: { min_length: 16 },
    });
    expect(passwordMinLengthFrom(tooWeak)).toBe(16);
    expect(
      passwordMinLengthFrom(new AppError({ code: "validation_failed", message: "x", httpStatus: 422 })),
    ).toBeNull();
    expect(passwordMinLengthFrom(new TypeError("network"))).toBeNull();
  });
});

describe("tenant selection", () => {
  it("reads a login's 409 tenant_required into the tenants and token", async () => {
    const tenants = [
      { slug: "acme", name: "Acme Corp" },
      { slug: "globex", name: "Globex" },
    ];
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(409, {
          error: { code: "tenant_required", message: "choose", details: { tenants, selection_token: "tok" } },
        }),
      ),
    );

    const err = await login({ email: "ada@example.com", password: "pw" }).catch((e: unknown) => e);
    expect(tenantSelectionFrom(err)).toEqual({ tenants, selectionToken: "tok" });
  });

  it("returns null for any other error", () => {
    expect(
      tenantSelectionFrom(new AppError({ code: "invalid_credentials", message: "no", httpStatus: 401 })),
    ).toBeNull();
    expect(tenantSelectionFrom(new TypeError("network"))).toBeNull();
  });

  it("posts the pick and returns the handoff", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { handoff: { host: "acme.goerp.io", code: "c0de" } }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await selectTenant("tok", "acme")).toEqual({
      kind: "handoff",
      handoff: { host: "acme.goerp.io", code: "c0de" },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/select-tenant",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ selection_token: "tok", tenant: "acme" }),
      }),
    );
  });

  it("rejects a spent token with its AppError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(401, { error: { code: "auth.selection_token_invalid", message: "expired" } })),
    );

    await expect(selectTenant("spent", "acme")).rejects.toMatchObject({ code: "auth.selection_token_invalid" });
  });
});

describe("register", () => {
  it("resolves the handoff a shared-domain registration answers with", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(201, { tenant_slug: "acme-corp", handoff: { host: "acme-corp.localhost", code: "c0de" } }),
      ),
    );

    await expect(
      register({ name: "Kwame Mensah", email: "kwame@acme.test", password: "pw", companyName: "Acme Corp" }),
    ).resolves.toEqual({
      kind: "handoff",
      tenantSlug: "acme-corp",
      handoff: { host: "acme-corp.localhost", code: "c0de" },
    });
  });

  const input = { name: "Kwame Mensah", email: "kwame@acme.test", password: "pw", companyName: "Acme Corp" };

  it("posts the form in the API's field names and resolves signed_in on a 201", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(201, { tenant_slug: "acme-corp", expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(register(input)).resolves.toEqual({ kind: "signed_in", tenantSlug: "acme-corp" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/register",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({
          name: "Kwame Mensah",
          email: "kwame@acme.test",
          password: "pw",
          company_name: "Acme Corp",
        }),
      }),
    );
  });

  it("resolves login_required on a 201 that issued no session", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(201, { tenant_slug: "acme-corp", login_required: true })),
    );
    await expect(register(input)).resolves.toEqual({ kind: "login_required", tenantSlug: "acme-corp" });
  });

  it("resolves verification_required on a 202 that requires verification, pending or not", async () => {
    for (const body of [
      { requires_email_verification: true, tenant_slug: "acme-corp" },
      { requires_email_verification: true, provisioning_pending: true, tenant_slug: "acme-corp" },
    ]) {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => jsonResponse(202, body)),
      );
      await expect(register(input)).resolves.toEqual({ kind: "verification_required", tenantSlug: "acme-corp" });
    }
  });

  it("resolves provisioning_pending on a 202 without verification", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(202, { requires_email_verification: false, provisioning_pending: true, tenant_slug: "acme-corp" }),
      ),
    );
    await expect(register(input)).resolves.toEqual({ kind: "provisioning_pending", tenantSlug: "acme-corp" });
  });

  it("carries a 422's per-field messages in details", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(422, {
          error: {
            code: "validation_failed",
            message: "some fields are invalid",
            details: { email: "Enter a valid email." },
          },
        }),
      ),
    );
    await expect(register(input)).rejects.toMatchObject({
      code: "validation_failed",
      httpStatus: 422,
      details: { email: "Enter a valid email." },
    });
  });
});

describe("checkSlug", () => {
  it("GETs the slug check and resolves its availability", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { available: false }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(checkSlug("acme-corp")).resolves.toBe(false);
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/check-slug?slug=acme-corp",
      expect.objectContaining({ credentials: "include" }),
    );
  });
});

describe("fetchInviteInfo", () => {
  it("GETs the info endpoint with token and tenant and maps the response", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(200, {
        tenant_name: "Acme Corp",
        email: "kwame@acme.com",
        name: null,
        password_required: true,
        password_min_length: 16,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(fetchInviteInfo({ token: "a+b", tenant: "acme" })).resolves.toEqual({
      tenantName: "Acme Corp",
      email: "kwame@acme.com",
      name: null,
      passwordRequired: true,
      passwordMinLength: 16,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/accept-invite/info?token=a%2Bb&tenant=acme",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("throws an AppError on a dead link", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(404, { error: { code: "invalid_invite", message: "invite link is invalid" } })),
    );
    await expect(fetchInviteInfo({ token: "t", tenant: "acme" })).rejects.toMatchObject({
      code: "invalid_invite",
      httpStatus: 404,
    });
  });
});

describe("acceptInvite", () => {
  it("posts token, tenant, and password, resolving signed_in for a session response", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(acceptInvite({ token: "t", tenant: "acme", password: "pw" })).resolves.toBe("signed_in");
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/accept-invite",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ token: "t", tenant: "acme", password: "pw" }),
      }),
    );
  });

  it("omits the password for an existing account and resolves login_required", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { login_required: true }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(acceptInvite({ token: "t", tenant: "acme" })).resolves.toBe("login_required");
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/accept-invite",
      expect.objectContaining({ body: JSON.stringify({ token: "t", tenant: "acme" }) }),
    );
  });
});

describe("session management", () => {
  it("fetchSessions maps each session", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(200, {
        sessions: [
          {
            id: "fam-1",
            user_agent: "Mozilla/5.0",
            ip_address: "41.66.18.2",
            country_code: "GH",
            signed_in_at: "2026-09-01T10:00:00Z",
            last_active_at: "2026-09-26T09:00:00Z",
            persistent: true,
            current: true,
          },
        ],
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    expect(await fetchSessions()).toEqual([
      {
        id: "fam-1",
        userAgent: "Mozilla/5.0",
        ipAddress: "41.66.18.2",
        countryCode: "GH",
        signedInAt: "2026-09-01T10:00:00Z",
        lastActiveAt: "2026-09-26T09:00:00Z",
        persistent: true,
        current: true,
      },
    ]);
    expect(fetchMock).toHaveBeenCalledWith("/auth/sessions", expect.objectContaining({ credentials: "include" }));
  });

  it("revokeSession deletes the session family", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(204, null));
    vi.stubGlobal("fetch", fetchMock);

    await revokeSession("fam-2");

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/sessions/fam-2",
      expect.objectContaining({ method: "DELETE", credentials: "include" }),
    );
  });

  it("revokeSession rejects with the server's error code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(400, {
          error: { code: "cannot_revoke_current_session", message: "sign out to end the current session" },
        }),
      ),
    );
    await expect(revokeSession("fam-1")).rejects.toMatchObject({
      code: "cannot_revoke_current_session",
      httpStatus: 400,
    });
  });

  it("revokeOtherSessions resolves to the number revoked", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { revoked: 3 }));
    vi.stubGlobal("fetch", fetchMock);

    expect(await revokeOtherSessions()).toBe(3);
    expect(fetchMock).toHaveBeenCalledWith("/auth/sessions", expect.objectContaining({ method: "DELETE" }));
  });
});

describe("logout", () => {
  it("posts to /auth/logout", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    await logout();

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/logout",
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
  });

  it("does not throw when the request fails outright", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("network down");
      }),
    );

    await expect(logout()).resolves.toBeUndefined();
  });
});
