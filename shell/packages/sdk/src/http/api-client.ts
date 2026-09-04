import { authMachine } from "../auth/auth-machine.js";
import { AppError } from "../error/app-error.js";
import type { APIClient, APIClientConfig, RefreshedTokens, RequestOptions } from "./types.js";

const MAX_NETWORK_RETRIES = 3;
const RETRY_BASE_DELAY_MS = 300;

// Methods erp-design.md §4.4.6 lets a caller de-duplicate via an
// Idempotency-Key header — generated once per logical call (below) and
// reused across every network-error retry and the one post-refresh retry
// of that same call, so a lost response never risks a duplicate side
// effect (e.g. a duplicate create) the way retrying a bare POST/PUT/PATCH
// otherwise would.
const IDEMPOTENT_KEYED_METHODS = new Set(["POST", "PUT", "PATCH"]);

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function buildURL(path: string, params?: Record<string, unknown>): string {
  if (!params) return path;
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue;
    search.set(key, String(value));
  }
  const query = search.toString();
  return query ? `${path}?${query}` : path;
}

async function fetchWithNetworkRetry(url: string, init: RequestInit, baseDelayMs: number): Promise<Response> {
  let attempt = 0;
  for (;;) {
    try {
      return await fetch(url, init);
    } catch (err) {
      attempt += 1;
      if (attempt > MAX_NETWORK_RETRIES) throw err;
      await delay(baseDelayMs * 2 ** (attempt - 1));
    }
  }
}

interface ErrorBody {
  code: string;
  message: string;
  details: Record<string, unknown> | undefined;
  requestId: string | undefined;
  traceId: string | undefined;
}

// Matches erp-design.md §4.4.5's error envelope exactly:
// { error: { code, message, details?, request_id?, trace_id? } }.
async function readErrorBody(response: Response): Promise<ErrorBody> {
  try {
    const body = (await response.json()) as {
      error?: {
        code?: string;
        message?: string;
        details?: Record<string, unknown>;
        request_id?: string;
        trace_id?: string;
      };
    };
    return {
      code: body.error?.code ?? "unknown_error",
      message: body.error?.message ?? (response.statusText || "request failed"),
      details: body.error?.details,
      requestId: body.error?.request_id,
      traceId: body.error?.trace_id,
    };
  } catch {
    return { code: "unknown_error", message: response.statusText || "request failed", details: undefined, requestId: undefined, traceId: undefined };
  }
}

function toAppError(response: Response, body: ErrorBody): AppError {
  return new AppError({
    code: body.code,
    message: body.message,
    httpStatus: response.status,
    details: body.details ?? null,
    requestId: body.requestId ?? null,
    traceId: body.traceId ?? null,
    // A 422's error.details IS the field-error map on the wire; AppError's
    // own constructor already nulls this out for every other status.
    fieldErrors: (body.details as Record<string, string[]> | undefined) ?? null,
  });
}

// Coalesces concurrent 401s into a single POST /auth/refresh call.
// auth-internals.md §4 "Refresh token rotation" makes this a correctness
// requirement, not just an optimization: two concurrent refresh calls
// presenting the same not-yet-rotated token race a `SELECT ... FOR
// UPDATE`, and the loser gets back a bare 401 for that /auth/refresh
// call itself (same-device race, step 5b) — so firing one independent
// refresh per racing request would spuriously log the user out on
// whichever requests lost that race.
let refreshInFlight: Promise<boolean> | null = null;

function ensureRefreshed(config: APIClientConfig): Promise<boolean> {
  refreshInFlight ??= performRefresh(config).finally(() => {
    refreshInFlight = null;
  });
  return refreshInFlight;
}

async function performRefresh(config: APIClientConfig): Promise<boolean> {
  const isCli = config.clientType === "cli";
  try {
    const headers: Record<string, string> = {};
    if (isCli) {
      headers["X-Client-Type"] = "cli";
      const refreshToken = config.getRefreshToken?.();
      if (refreshToken) headers.Authorization = `Bearer ${refreshToken}`;
    }
    const response = await fetch("/auth/refresh", {
      method: "POST",
      credentials: isCli ? "omit" : "include",
      headers,
    });
    if (!response.ok) return false;

    if (isCli) {
      const body = (await response.json()) as {
        access_token: string;
        refresh_token: string;
        expires_in: number;
      };
      const tokens: RefreshedTokens = {
        accessToken: body.access_token,
        refreshToken: body.refresh_token,
        expiresIn: body.expires_in,
      };
      config.onTokensRefreshed?.(tokens);
    }
    return true;
  } catch {
    return false;
  }
}

export class FetchAPIClient implements APIClient {
  constructor(private readonly config: APIClientConfig = {}) {}

  get<T>(path: string, options?: RequestOptions): Promise<T> {
    return this.requestJSON<T>("GET", path, undefined, options);
  }

  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return this.requestJSON<T>("POST", path, body, options);
  }

  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return this.requestJSON<T>("PUT", path, body, options);
  }

  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return this.requestJSON<T>("PATCH", path, body, options);
  }

  delete<T>(path: string, options?: RequestOptions): Promise<T> {
    return this.requestJSON<T>("DELETE", path, undefined, options);
  }

  async getBlob(path: string, options?: RequestOptions): Promise<Blob> {
    const response = await this.send("GET", path, undefined, options);
    return response.blob();
  }

  async postFormData<T>(path: string, data: Record<string, unknown>): Promise<T> {
    const formData = new FormData();
    for (const [key, value] of Object.entries(data)) {
      if (value === undefined || value === null) continue;
      formData.append(key, value instanceof Blob ? value : String(value));
    }
    const response = await this.send("POST", path, formData);
    return parseJSON<T>(response);
  }

  private async requestJSON<T>(
    method: string,
    path: string,
    body: unknown,
    options?: RequestOptions,
  ): Promise<T> {
    const response = await this.send(method, path, body, options);
    return parseJSON<T>(response);
  }

  private async send(method: string, path: string, body: unknown, options?: RequestOptions): Promise<Response> {
    const url = buildURL(path, options?.params);
    const isFormData = body instanceof FormData;
    const idempotencyKey =
      IDEMPOTENT_KEYED_METHODS.has(method) && !options?.headers?.["Idempotency-Key"]
        ? crypto.randomUUID()
        : undefined;

    const buildInit = (): RequestInit => {
      const headers: Record<string, string> = { ...options?.headers };
      if (body !== undefined && !isFormData) headers["Content-Type"] = "application/json";
      if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
      if (this.config.clientType === "cli") {
        headers["X-Client-Type"] = "cli";
        const token = this.config.getAccessToken?.();
        if (token) headers.Authorization = `Bearer ${token}`;
      }
      const init: RequestInit = {
        method,
        headers,
        credentials: this.config.clientType === "cli" ? "omit" : "include",
      };
      if (body !== undefined) init.body = isFormData ? (body as FormData) : JSON.stringify(body);
      if (options?.signal) init.signal = options.signal;
      return init;
    };

    const baseDelayMs = this.config.retryBaseDelayMs ?? RETRY_BASE_DELAY_MS;
    let response = await fetchWithNetworkRetry(url, buildInit(), baseDelayMs);

    // Never trigger the refresh cycle for a 401 from /auth/refresh itself.
    if (response.status === 401 && path !== "/auth/refresh") {
      const refreshed = await ensureRefreshed(this.config);
      if (!refreshed) {
        authMachine.transition({ type: "session_expired" });
        throw toAppError(response, await readErrorBody(response));
      }
      // Exactly one retry — whatever it returns (even another 401) is
      // final; this does not loop back into another refresh attempt.
      response = await fetchWithNetworkRetry(url, buildInit(), baseDelayMs);
    }

    if (!response.ok) {
      throw toAppError(response, await readErrorBody(response));
    }
    return response;
  }
}

async function parseJSON<T>(response: Response): Promise<T> {
  const text = await response.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

// The browser shell's shared client — zero setup required, matching
// auth-machine.ts's own singleton (this module fires
// authMachine.transition({ type: "session_expired" }) directly on an
// unrecoverable 401, the same "fetch wrapper" shell-architecture.md §7
// describes). A non-browser consumer (no such caller exists in this repo
// yet) instantiates its own FetchAPIClient with clientType: "cli" instead
// of using this instance.
export const apiClient: APIClient = new FetchAPIClient();
