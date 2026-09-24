export const DEFAULT_POST_LOGIN_PATH = "/";

// /auth/* pages are reachable without a session and never chrome-wrapped.
export function isAuthPath(pathname: string): boolean {
  return pathname === "/auth" || pathname.startsWith("/auth/");
}

// Accepts only a same-origin absolute path: "//host" and "/\host" are
// protocol-relative to browsers, and an /auth/* target would bounce straight
// back into the auth flow.
export function safeRedirect(raw: unknown): string {
  if (typeof raw !== "string" || !raw.startsWith("/")) return DEFAULT_POST_LOGIN_PATH;
  if (raw.startsWith("//") || raw.startsWith("/\\")) return DEFAULT_POST_LOGIN_PATH;
  if (isAuthPath(raw.split(/[?#]/, 1)[0] ?? raw)) return DEFAULT_POST_LOGIN_PATH;
  return raw;
}
