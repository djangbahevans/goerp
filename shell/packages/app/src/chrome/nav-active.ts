import { useRouterState } from "@tanstack/react-router";
import type { NavigationGroup, NavigationItem } from "./navigation-types.js";

export interface NavLocation {
  pathname: string;
  search: Record<string, unknown>;
}

// The query parameters an item stands for: its route's own, and its default_filters as the
// `filter[...]` parameters a list writes to the URL once they are applied.
export function navItemParams(item: NavigationItem): Record<string, string> {
  const params = { ...item.search };
  for (const [field, value] of Object.entries(item.defaultFilters ?? {})) {
    if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
      params[`filter[${field}]`] = String(value);
    }
  }
  return params;
}

function matches(item: NavigationItem, location: NavLocation): boolean {
  if (item.external) return false;
  if (location.pathname !== item.path && !location.pathname.startsWith(`${item.path}/`)) return false;
  return Object.entries(navItemParams(item)).every(([key, value]) => String(location.search[key]) === value);
}

// The one item that is active: of those whose path and query the location satisfies, the one with
// the longest path, then the most parameters, so "Customers" beats "All Contacts" on a filtered list.
export function activeNavItemKey(tree: NavigationGroup[], location: NavLocation): string | undefined {
  let best: { key: string; pathLength: number; params: number } | undefined;
  for (const item of tree.flatMap((group) => group.children)) {
    if (!matches(item, location)) continue;
    const candidate = {
      key: item.key,
      pathLength: item.path.length,
      params: Object.keys(navItemParams(item)).length,
    };
    if (
      !best ||
      candidate.pathLength > best.pathLength ||
      (candidate.pathLength === best.pathLength && candidate.params > best.params)
    ) {
      best = candidate;
    }
  }
  return best?.key;
}

export function useActiveNavItemKey(tree: NavigationGroup[]): string | undefined {
  const pathname = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).pathname });
  const search = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).search }) as Record<
    string,
    unknown
  >;
  return activeNavItemKey(tree, { pathname, search });
}
