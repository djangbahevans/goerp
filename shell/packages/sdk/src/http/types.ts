export interface RequestOptions {
  params?: Record<string, unknown>;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export interface APIClient {
  get<T>(path: string, options?: RequestOptions): Promise<T>;
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>;
  delete<T>(path: string, options?: RequestOptions): Promise<T>;
  getBlob(path: string, options?: RequestOptions): Promise<Blob>;
  postFormData<T>(path: string, data: Record<string, unknown>, options?: RequestOptions): Promise<T>;
}

export interface RefreshedTokens {
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
}

export interface APIClientConfig {
  // "browser" (default): credentials: 'include', cookie-based auth.
  // "cli": X-Client-Type: cli plus an Authorization: Bearer header, per
  // auth-internals.md §19 — token storage itself is the caller's own.
  clientType?: "browser" | "cli";
  getAccessToken?: () => string | null;
  // cli only — the /auth/refresh call's own bearer is the refresh token.
  getRefreshToken?: () => string | null;
  // cli only — resent on every /auth/refresh call (auth-internals.md §19).
  getDeviceId?: () => string | null;
  onTokensRefreshed?: (tokens: RefreshedTokens) => void;
  // Base delay (ms) between network-error retries, doubling each attempt.
  // Defaults to 300ms — overridable so tests don't wait on real backoff.
  retryBaseDelayMs?: number;
}
