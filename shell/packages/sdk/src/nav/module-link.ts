export const MODULE_PATH_PREFIX = "/_m";

export function moduleLink(expandedApiPath: string): string {
  return MODULE_PATH_PREFIX + expandedApiPath;
}
