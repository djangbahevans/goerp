// shell-architecture.md's browser-path-vs-API-path rule: module view
// routes live under /_m/*; module authors work in API-path terms
// ("/contacts", "/contacts/01j...") and the shell adds this prefix for
// browser navigation.
export const MODULE_PATH_PREFIX = "/_m";

export function moduleLink(expandedApiPath: string): string {
  return MODULE_PATH_PREFIX + expandedApiPath;
}
