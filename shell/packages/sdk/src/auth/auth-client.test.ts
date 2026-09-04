import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { fetchCurrentSession, login, logout, submitMFACode } from "./auth-client.js";

function jsonResponse(status: number, body: unknown, statusText = ""): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText,
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
        user: { id: "u1", email: "a@example.com", roles: ["admin"], amr: ["pwd"], mfa_verified_at: null },
        tenant: { id: "t1", slug: "acme", name: "Acme", plan: "pro" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const session = await fetchCurrentSession();

    expect(session).toEqual({
      user: { id: "u1", email: "a@example.com", roles: ["admin"], amr: ["pwd"], mfaVerifiedAt: null },
      tenant: { id: "t1", slug: "acme", name: "Acme", plan: "pro" },
    });
    expect(fetchMock).toHaveBeenCalledWith("/auth/me", { credentials: "include" });
  });

  it("returns null on 401", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(401, { error: { code: "unauthenticated" } })));
    expect(await fetchCurrentSession()).toBeNull();
  });

  it("returns null on any other non-2xx status", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(500, {})));
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

  it("rejects with an AppError even when the error body isn't JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
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
