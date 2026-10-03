import { authMachine } from "../auth/auth-machine.js";
import { sessionActivity } from "../auth/session-activity.js";
import { noteTenantSuspension } from "../auth/tenant-suspension.js";
import { AppError } from "../error/app-error.js";
import type {
  APIClient,
  APIClientConfig,
  DeleteOptions,
  RefreshedTokens,
  RefreshOutcome,
  RequestOptions,
  SessionRefresher,
} from "./types.js";

const MAX_NETWORK_RETRIES = 3;
const RETRY_BASE_DELAY_MS = 300;

const IDEMPOTENT_KEYED_METHODS = new Set(["POST", "PUT", "PATCH"]);

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function buildURL(path: string, params?: Record<string, unknown>): string {
  if (!params) return path;
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue;
    if (Array.isArray(value)) {
      for (const item of value) search.append(key, String(item));
      continue;
    }
    search.set(key, String(value));
  }
  const query = search.toString();
  if (!query) return path;
  return path.includes("?") ? `${path}&${query}` : `${path}?${query}`;
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
    return {
      code: "unknown_error",
      message: response.statusText || "request failed",
      details: undefined,
      requestId: undefined,
      traceId: undefined,
    };
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
    // A 422's error.details IS the field-error map on the wire.
    fieldErrors: (body.details as Record<string, string[]> | undefined) ?? null,
  });
}

export class FetchAPIClient implements APIClient, SessionRefresher {
  private refreshInFlight: Promise<RefreshOutcome> | null = null;
  private readonly refreshListeners = new Set<(expiresIn: number) => void>();

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

  delete<T>(path: string, options?: DeleteOptions): Promise<T> {
    return this.requestJSON<T>("DELETE", path, options?.body, options);
  }

  async getBlob(path: string, options?: RequestOptions): Promise<Blob> {
    return (await this.getBlobWithHeaders(path, options)).blob;
  }

  async getBlobWithHeaders(path: string, options?: RequestOptions): Promise<{ blob: Blob; headers: Headers }> {
    const response = await this.send("GET", path, undefined, options);
    return { blob: await response.blob(), headers: response.headers };
  }

  async postFormData<T>(path: string, data: Record<string, unknown>, options?: RequestOptions): Promise<T> {
    const formData = new FormData();
    for (const [key, value] of Object.entries(data)) {
      if (value === undefined || value === null) continue;
      formData.append(key, value instanceof Blob ? value : String(value));
    }
    const response = await this.send("POST", path, formData, options);
    return parseJSON<T>(response);
  }

  private async requestJSON<T>(method: string, path: string, body: unknown, options?: RequestOptions): Promise<T> {
    const response = await this.send(method, path, body, options);
    return parseJSON<T>(response);
  }

  private async send(method: string, path: string, body: unknown, options?: RequestOptions): Promise<Response> {
    const canRefresh = this.config.clientType === "cli" || (!options?.background && sessionActivity.hasActivity());
    const url = buildURL(path, options?.params);
    const isFormData = body instanceof FormData;
    const idempotencyKey =
      IDEMPOTENT_KEYED_METHODS.has(method) && !options?.headers?.["Idempotency-Key"] ? crypto.randomUUID() : undefined;

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

    if (response.status === 401 && path !== "/auth/refresh") {
      if (!canRefresh) throw toAppError(response, await readErrorBody(response));
      const refreshed = await this.ensureRefreshed(baseDelayMs);
      if (!refreshed.ok) {
        authMachine.transition({ type: "session_expired" });
        throw toAppError(response, await readErrorBody(response));
      }
      response = await fetchWithNetworkRetry(url, buildInit(), baseDelayMs);
      if (response.status === 401) {
        authMachine.transition({ type: "session_expired" });
      }
    }

    if (!response.ok) {
      const body = await readErrorBody(response);
      if (response.status === 403 && body.code === "mfa_setup_required") {
        authMachine.transition({ type: "mfa_setup_required" });
      }
      const error = toAppError(response, body);
      noteTenantSuspension(error);
      throw error;
    }
    return response;
  }

  refreshSession(): Promise<RefreshOutcome> {
    return this.ensureRefreshed(this.config.retryBaseDelayMs ?? RETRY_BASE_DELAY_MS);
  }

  subscribeRefresh(listener: (expiresIn: number) => void): () => void {
    this.refreshListeners.add(listener);
    return () => this.refreshListeners.delete(listener);
  }

  // Rotation is single-use; concurrent refreshes must share the same request to avoid a false logout.
  private ensureRefreshed(baseDelayMs: number): Promise<RefreshOutcome> {
    this.refreshInFlight ??= this.performRefresh(baseDelayMs).finally(() => {
      this.refreshInFlight = null;
    });
    return this.refreshInFlight;
  }

  private async performRefresh(baseDelayMs: number): Promise<RefreshOutcome> {
    const activity = sessionActivity.snapshot();
    const isCli = this.config.clientType === "cli";
    try {
      const headers: Record<string, string> = {};
      if (isCli) {
        headers["X-Client-Type"] = "cli";
        const refreshToken = this.config.getRefreshToken?.();
        if (refreshToken) headers.Authorization = `Bearer ${refreshToken}`;
        // A missing or mismatched device ID on a replay revokes the entire session family.
        const deviceId = this.config.getDeviceId?.();
        if (deviceId) headers.device_id = deviceId;
      }
      const response = await fetchWithNetworkRetry(
        "/auth/refresh",
        { method: "POST", credentials: isCli ? "omit" : "include", headers },
        baseDelayMs,
      );
      if (!response.ok) return { ok: false };

      const body = (await response.json()) as {
        access_token?: string;
        refresh_token?: string;
        expires_in: number;
      };
      if (isCli && body.access_token && body.refresh_token) {
        const tokens: RefreshedTokens = {
          accessToken: body.access_token,
          refreshToken: body.refresh_token,
          expiresIn: body.expires_in,
        };
        this.config.onTokensRefreshed?.(tokens);
      }
      if (!isCli) sessionActivity.acknowledge(activity);
      for (const listener of this.refreshListeners) listener(body.expires_in);
      return { ok: true, expiresIn: body.expires_in };
    } catch {
      return { ok: false };
    }
  }
}

async function parseJSON<T>(response: Response): Promise<T> {
  const text = await response.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const apiClient: APIClient & SessionRefresher = new FetchAPIClient();
