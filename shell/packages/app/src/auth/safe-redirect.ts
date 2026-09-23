export const DEFAULT_POST_LOGIN_PATH = "/";

// Accepts only a same-origin absolute path: "//host" and "/\host" are
// protocol-relative to browsers, and an /auth/* target would bounce straight
// back into the auth flow.
export function safeRedirect(raw: unknown): string {
  if (typeof raw !== "string" || !raw.startsWith("/")) return DEFAULT_POST_LOGIN_PATH;
  if (raw.startsWith("//") || raw.startsWith("/\\")) return DEFAULT_POST_LOGIN_PATH;
  if (raw === "/auth" || raw.startsWith("/auth/")) return DEFAULT_POST_LOGIN_PATH;
  return raw;
}
