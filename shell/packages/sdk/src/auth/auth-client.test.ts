import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import {
  fetchCurrentSession,
  fetchTenantContext,
  login,
  logout,
  requestPasswordReset,
  submitMFACode,
  updateProfile,
} from "./auth-client.js";

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
        },
        tenant: { id: "t1", slug: "acme", name: "Acme", plan: "pro" },
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
      },
      tenant: { id: "t1", slug: "acme", name: "Acme", plan: "pro" },
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

describe("login", () => {
  const credentials = { email: "a@example.com", password: "hunter2", tenant: "acme" };

  it("returns authenticated for a full-session response", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await login(credentials);

    expect(result).toEqual({ kind: "authenticated" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/login",
      expect.objectContaining({ method: "POST", credentials: "include" }),
    );
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
      vi.fn(async () => jsonResponse(200, { tenant: { slug: "acme", name: "Acme Corp" }, registration_enabled: true })),
    );

    expect(await fetchTenantContext()).toEqual({
      tenant: { slug: "acme", name: "Acme Corp" },
      registrationEnabled: true,
    });
  });

  it("maps a shared-domain response's null tenant", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(200, { tenant: null, registration_enabled: false })),
    );

    expect(await fetchTenantContext()).toEqual({ tenant: null, registrationEnabled: false });
  });

  it("resolves to null on a non-200 or network failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: { code: "tenant_suspended" } })),
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

describe("submitMFACode", () => {
  it("posts the challenge token, type, and code", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);

    await submitMFACode("mfa-tok", "123456", "totp");

    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/mfa/verify",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        body: JSON.stringify({ mfa_token: "mfa-tok", type: "totp", code: "123456" }),
      }),
    );
  });

  it("throws an AppError on an invalid code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(401, { error: { code: "invalid_mfa_code", message: "invalid MFA code" } })),
    );

    await expect(submitMFACode("mfa-tok", "000000", "totp")).rejects.toMatchObject({
      code: "invalid_mfa_code",
    });
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
