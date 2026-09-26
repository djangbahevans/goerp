import type { SignInHandoff } from "@goerp/sdk/auth";
import { DEFAULT_POST_LOGIN_PATH } from "./safe-redirect.js";

// The tenant host's /auth/handoff page, on the current page's scheme and
// port (shell-ux.md §2.11), carrying the already-validated post-sign-in
// path along.
export function handoffURL(
  handoff: SignInHandoff,
  redirectTo: string,
  current: Pick<Location, "protocol" | "port"> = window.location,
): string {
  const params = new URLSearchParams({ code: handoff.code });
  if (redirectTo !== DEFAULT_POST_LOGIN_PATH) params.set("redirect", redirectTo);
  const port = current.port ? `:${current.port}` : "";
  return `${current.protocol}//${handoff.host}${port}/auth/handoff?${params}`;
}
