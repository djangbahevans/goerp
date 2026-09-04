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
  postFormData<T>(path: string, data: Record<string, unknown>): Promise<T>;
}

export interface RefreshedTokens {
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
}

export interface APIClientConfig {
  // "browser" (default) sends cookies (credentials: 'include') and lets
  // the __Host-access_token/refresh_token cookies carry auth. "cli" sends
  // X-Client-Type: cli and an Authorization: Bearer header instead — the
  // token itself is this config's caller's responsibility to store
  // (auth-internals.md §19 "Token storage strategy": OS keychain,
  // in-memory — never this client's job).
  clientType?: "browser" | "cli";
  getAccessToken?: () => string | null;
  // Only consulted for the cli client type's own POST /auth/refresh call
  // (auth-internals.md §4 "Refresh token rotation" sends the refresh
  // token, not the access token, as that request's own bearer).
  getRefreshToken?: () => string | null;
  onTokensRefreshed?: (tokens: RefreshedTokens) => void;
  // Base delay (ms) between network-error retries, doubling each attempt.
  // Defaults to 300ms — overridable so tests don't wait on real backoff.
  retryBaseDelayMs?: number;
}
