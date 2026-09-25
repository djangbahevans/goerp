import { isServerFailure } from "./route-error.js";

// shell-ux.md §1 "Error pages (no chrome)".
const ERROR_PATHS = new Set(["/403", "/404", "/tenant-suspended"]);

export function isErrorPath(pathname: string): boolean {
  return ERROR_PATHS.has(pathname);
}

interface MatchOutcome {
  status: string;
  error: unknown;
  _notFound?: boolean | undefined;
}

// Whether the router is rendering the 404 or 500 page in place of a route
// that failed to match or load, which drops the chrome the same way the
// error routes themselves do.
export function rendersErrorPage(matches: readonly MatchOutcome[]): boolean {
  return matches.some(
    (m) => m._notFound === true || m.status === "notFound" || (m.status === "error" && isServerFailure(m.error)),
  );
}
