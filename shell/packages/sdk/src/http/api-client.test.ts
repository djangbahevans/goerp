import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { authMachine } from "../auth/auth-machine.js";
import { FetchAPIClient } from "./api-client.js";

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    json: async () => body,
    text: async () => JSON.stringify(body),
    blob: async () => new Blob([JSON.stringify(body)]),
  } as unknown as Response;
}

function emptyResponse(status: number): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    json: async () => {
      throw new Error("no body");
    },
    text: async () => "",
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("FetchAPIClient basic requests", () => {
  it("sends GET with credentials included and no body", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, { id: "1" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    const result = await client.get<{ id: string }>("/contacts/1");

    expect(result).toEqual({ id: "1" });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/contacts/1");
    expect(init.method).toBe("GET");
    expect(init.credentials).toBe("include");
    expect(init.body).toBeUndefined();
  });

  it("appends query params for GET", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, []));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await client.get("/contacts", { params: { q: "acme", limit: 10, missing: undefined } });

    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/contacts?q=acme&limit=10");
  });

  it("uses & instead of a second ? when the path already has a query string", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, []));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await client.get("/contacts?sort=name", { params: { limit: 10 } });

    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/contacts?sort=name&limit=10");
  });

  it("sends POST with a JSON body and Content-Type header", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(201, { id: "1" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await client.post("/contacts", { name: "Acme" });

    const [, init] = fetchMock.mock.calls[0]!;
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ name: "Acme" }));
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("handles an empty response body (e.g. 204 from delete)", async () => {
    const fetchMock = vi.fn(async () => emptyResponse(204));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    const result = await client.delete("/contacts/1");
    expect(result).toBeUndefined();
  });

  it("getBlob returns a Blob without JSON parsing", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(200, { data: "x" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    const blob = await client.getBlob("/contacts/export");
    expect(blob).toBeInstanceOf(Blob);
  });

  it("postFormData builds a FormData body without a Content-Type header", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, { jobId: "j1" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();
    const file = new Blob(["hello"]);

    await client.postFormData("/contacts/import", { file, note: "batch 1" });

    const [, init] = fetchMock.mock.calls[0]!;
    expect(init.body).toBeInstanceOf(FormData);
    const formData = init.body as FormData;
    expect(formData.get("file")).toBeInstanceOf(Blob);
    expect(formData.get("note")).toBe("batch 1");
    expect((init.headers as Record<string, string>)["Content-Type"]).toBeUndefined();
  });

  it("postFormData forwards options (signal, custom headers)", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, { jobId: "j1" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();
    const controller = new AbortController();

    await client.postFormData(
      "/contacts/import",
      { file: new Blob(["x"]) },
      { signal: controller.signal, headers: { "X-Custom": "1" } },
    );

    const [, init] = fetchMock.mock.calls[0]!;
    expect(init.signal).toBe(controller.signal);
    expect((init.headers as Record<string, string>)["X-Custom"]).toBe("1");
  });
});

describe("FetchAPIClient error deserialization", () => {
  it("throws an AppError for a non-2xx response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(404, { error: { code: "not_found", message: "no such contact", request_id: "req_1" } }),
      ),
    );
    const client = new FetchAPIClient();

    await expect(client.get("/contacts/x")).rejects.toMatchObject({
      code: "not_found",
      httpStatus: 404,
      requestId: "req_1",
    });
  });

  it("populates fieldErrors from details on a 422", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(422, {
          error: { code: "validation_failed", message: "invalid", details: { email: ["Invalid email"] } },
        }),
      ),
    );
    const client = new FetchAPIClient();

    const err = await client.post("/contacts", {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AppError);
    expect((err as AppError).fieldErrors).toEqual({ email: ["Invalid email"] });
  });
});

describe("FetchAPIClient silent refresh on 401", () => {
  it("refreshes once and retries the original request on success", async () => {
    const calls: string[] = [];
    const fetchMock = vi.fn(async (url: string) => {
      calls.push(url);
      if (url === "/contacts/1") {
        // First call 401s, the post-refresh retry succeeds.
        return calls.filter((c) => c === "/contacts/1").length === 1
          ? jsonResponse(401, { error: { code: "unauthenticated" } })
          : jsonResponse(200, { id: "1" });
      }
      if (url === "/auth/refresh") return jsonResponse(200, { expires_in: 900 });
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    const result = await client.get<{ id: string }>("/contacts/1");

    expect(result).toEqual({ id: "1" });
    expect(calls.filter((c) => c === "/auth/refresh")).toHaveLength(1);
    expect(calls.filter((c) => c === "/contacts/1")).toHaveLength(2);
  });

  it("transitions to unauthenticated and does not loop when refresh itself fails", async () => {
    const transitionSpy = vi.spyOn(authMachine, "transition");
    const fetchMock = vi.fn(async (url: string) => {
      if (url === "/auth/refresh") return jsonResponse(401, { error: { code: "invalid_refresh_token" } });
      return jsonResponse(401, { error: { code: "unauthenticated" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await expect(client.get("/contacts/1")).rejects.toMatchObject({ httpStatus: 401 });

    expect(transitionSpy).toHaveBeenCalledWith({ type: "session_expired" });
    // Exactly one call to the original path (no retry attempted after a failed refresh).
    expect(fetchMock.mock.calls.filter(([url]) => url === "/contacts/1")).toHaveLength(1);
    expect(fetchMock.mock.calls.filter(([url]) => url === "/auth/refresh")).toHaveLength(1);
  });

  it("coalesces concurrent 401s into a single refresh call", async () => {
    let refreshCalls = 0;
    const firstAttemptDone = new Set<string>();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/auth/refresh") {
          refreshCalls += 1;
          return jsonResponse(200, { expires_in: 900 });
        }
        // Each distinct URL 401s on its own first attempt, then succeeds.
        if (!firstAttemptDone.has(url)) {
          firstAttemptDone.add(url);
          return jsonResponse(401, { error: { code: "unauthenticated" } });
        }
        return jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient();

    const [a, b] = await Promise.all([client.get("/contacts/1"), client.get("/contacts/2")]);

    expect(a).toEqual({ ok: true });
    expect(b).toEqual({ ok: true });
    expect(refreshCalls).toBe(1);
  });

  it("does not coalesce refreshes across two different client instances", async () => {
    let refreshCalls = 0;
    const firstAttemptDone = new Set<string>();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/auth/refresh") {
          refreshCalls += 1;
          return jsonResponse(200, { expires_in: 900 });
        }
        if (!firstAttemptDone.has(url)) {
          firstAttemptDone.add(url);
          return jsonResponse(401, { error: { code: "unauthenticated" } });
        }
        return jsonResponse(200, { ok: true });
      }),
    );
    const clientA = new FetchAPIClient();
    const clientB = new FetchAPIClient();

    await Promise.all([clientA.get("/contacts/1"), clientB.get("/contacts/2")]);

    expect(refreshCalls).toBe(2);
  });

  it("transitions to unauthenticated when the post-refresh retry still 401s", async () => {
    const transitionSpy = vi.spyOn(authMachine, "transition");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/auth/refresh") return jsonResponse(200, { expires_in: 900 });
        return jsonResponse(401, { error: { code: "unauthenticated" } });
      }),
    );
    const client = new FetchAPIClient();

    await expect(client.get("/contacts/1")).rejects.toMatchObject({ httpStatus: 401 });

    expect(transitionSpy).toHaveBeenCalledWith({ type: "session_expired" });
  });

  it("retries a network error during the refresh call itself", async () => {
    let refreshAttempts = 0;
    // /contacts/1 401s once to trigger the refresh path, then succeeds.
    let originalAttempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/auth/refresh") {
          refreshAttempts += 1;
          if (refreshAttempts < 2) throw new Error("network down");
          return jsonResponse(200, { expires_in: 900 });
        }
        originalAttempts += 1;
        return originalAttempts === 1
          ? jsonResponse(401, { error: { code: "unauthenticated" } })
          : jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient({ retryBaseDelayMs: 1 });

    const result = await client.get("/contacts/1");

    expect(result).toEqual({ ok: true });
    expect(refreshAttempts).toBe(2);
  });

  it("never triggers a refresh cycle for a 401 from /auth/refresh itself", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(401, { error: { code: "invalid_refresh_token" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await expect(client.post("/auth/refresh")).rejects.toMatchObject({ httpStatus: 401 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("FetchAPIClient.refreshSession (public, shared with TokenRefreshScheduler)", () => {
  it("returns the response's expires_in on success in browser mode", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(200, { expires_in: 900 })));
    const client = new FetchAPIClient();

    await expect(client.refreshSession()).resolves.toEqual({ ok: true, expiresIn: 900 });
  });

  it("returns ok: false on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(401, { error: { code: "invalid_refresh_token" } })));
    const client = new FetchAPIClient();

    await expect(client.refreshSession()).resolves.toEqual({ ok: false });
  });

  it("coalesces two concurrent refreshSession() calls into one request", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls += 1;
        return jsonResponse(200, { expires_in: 900 });
      }),
    );
    const client = new FetchAPIClient();

    const [a, b] = await Promise.all([client.refreshSession(), client.refreshSession()]);

    expect(a).toEqual({ ok: true, expiresIn: 900 });
    expect(b).toEqual({ ok: true, expiresIn: 900 });
    expect(calls).toBe(1);
  });
});

describe("FetchAPIClient network error retry", () => {
  it("retries a network failure up to 3 times before succeeding", async () => {
    let attempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        attempts += 1;
        if (attempts < 3) throw new Error("network down");
        return jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient({ retryBaseDelayMs: 1 });

    const result = await client.get("/contacts");

    expect(result).toEqual({ ok: true });
    expect(attempts).toBe(3);
  });

  it("gives up after exhausting retries", async () => {
    let attempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        attempts += 1;
        throw new Error("network down");
      }),
    );
    const client = new FetchAPIClient({ retryBaseDelayMs: 1 });

    await expect(client.get("/contacts")).rejects.toThrow("network down");
    // 1 initial attempt + 3 retries.
    expect(attempts).toBe(4);
  });

  it("reuses the same Idempotency-Key across retries of the same call", async () => {
    let attempts = 0;
    const seenKeys: (string | undefined)[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init: RequestInit) => {
        seenKeys.push((init.headers as Record<string, string>)["Idempotency-Key"]);
        attempts += 1;
        if (attempts < 2) throw new Error("network down");
        return jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient({ retryBaseDelayMs: 1 });

    await client.post("/contacts", { name: "Acme" });

    expect(seenKeys).toHaveLength(2);
    expect(seenKeys[0]).toBeDefined();
    expect(seenKeys[0]).toBe(seenKeys[1]);
  });

  it("does not attach an Idempotency-Key to GET requests", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, {}));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient();

    await client.get("/contacts");

    const [, init] = fetchMock.mock.calls[0]!;
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBeUndefined();
  });
});

describe("FetchAPIClient cli mode", () => {
  it("sends X-Client-Type and a bearer token instead of cookies", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new FetchAPIClient({ clientType: "cli", getAccessToken: () => "access-tok" });

    await client.get("/contacts");

    const [, init] = fetchMock.mock.calls[0]!;
    const headers = init.headers as Record<string, string>;
    expect(headers["X-Client-Type"]).toBe("cli");
    expect(headers.Authorization).toBe("Bearer access-tok");
    expect(init.credentials).toBe("omit");
  });

  it("refreshes with the refresh token, not the access token, and persists the new tokens", async () => {
    const onTokensRefreshed = vi.fn();
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/auth/refresh") {
        expect((init?.headers as Record<string, string>).Authorization).toBe("Bearer refresh-tok");
        return jsonResponse(200, {
          access_token: "new-access",
          refresh_token: "new-refresh",
          expires_in: 900,
        });
      }
      return jsonResponse(401, { error: { code: "unauthenticated" } });
    });
    let calledOriginalOnce = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === "/auth/refresh") return fetchMock(url, init);
        if (!calledOriginalOnce) {
          calledOriginalOnce = true;
          return jsonResponse(401, { error: { code: "unauthenticated" } });
        }
        return jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient({
      clientType: "cli",
      getAccessToken: () => "access-tok",
      getRefreshToken: () => "refresh-tok",
      onTokensRefreshed,
    });

    const result = await client.get("/contacts");

    expect(result).toEqual({ ok: true });
    expect(onTokensRefreshed).toHaveBeenCalledWith({
      accessToken: "new-access",
      refreshToken: "new-refresh",
      expiresIn: 900,
    });
  });

  it("sends device_id on the refresh call, per auth-internals.md §19", async () => {
    let calledOriginalOnce = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === "/auth/refresh") {
          expect((init?.headers as Record<string, string>).device_id).toBe("device-1");
          return jsonResponse(200, { access_token: "a", refresh_token: "r", expires_in: 900 });
        }
        if (!calledOriginalOnce) {
          calledOriginalOnce = true;
          return jsonResponse(401, { error: { code: "unauthenticated" } });
        }
        return jsonResponse(200, { ok: true });
      }),
    );
    const client = new FetchAPIClient({
      clientType: "cli",
      getRefreshToken: () => "refresh-tok",
      getDeviceId: () => "device-1",
    });

    await client.get("/contacts");
  });
});
