import type { PermissionContextValue } from "@goerp/sdk/auth";
import type { ViewRegistry } from "@goerp/sdk/schema";

export const MAX_PRELOADED_MODULES = 3;
const MAX_HISTORY = 10;

function historyKey(userId: string): string {
  return `goerp-module-history:${userId}`;
}

// Storage can be unavailable or full (private browsing, quota); history is only a ranking hint.
export function readModuleHistory(userId: string): string[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(historyKey(userId)) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((name): name is string => typeof name === "string") : [];
  } catch {
    return [];
  }
}

export function recordModuleVisit(userId: string, moduleName: string): void {
  const history = readModuleHistory(userId);
  if (history[0] === moduleName) return;
  const next = [moduleName, ...history.filter((name) => name !== moduleName)].slice(0, MAX_HISTORY);
  try {
    window.localStorage.setItem(historyKey(userId), JSON.stringify(next));
  } catch {}
}

export interface PredictionInput {
  registry: Pick<ViewRegistry, "navigationTree" | "resolveRoute">;
  permissions: Pick<PermissionContextValue, "check" | "moduleEnabled">;
  history: string[];
  exclude?: string | null;
}

// The modules behind the routes the user is most likely to open next: navigation routes of
// recently visited modules first (most recent first), then the rest in navigation order. Only
// routes the user can open and whose module ships a bundle count.
export function predictPreloadModules({ registry, permissions, history, exclude }: PredictionInput): string[] {
  const routes = registry.navigationTree.flatMap((group) => group.children);
  const rank = (path: string) => {
    const index = history.indexOf(registry.resolveRoute(path)?.module ?? "");
    return index < 0 ? history.length : index;
  };
  const ranked = routes.toSorted((a, b) => rank(a.path) - rank(b.path));

  const modules: string[] = [];
  for (const item of ranked) {
    if (modules.length === MAX_PRELOADED_MODULES) break;
    if (item.external || (item.permission !== undefined && !permissions.check(item.permission))) continue;
    const view = registry.resolveRoute(item.path);
    if (!view?.bundleUrl || view.module === exclude || modules.includes(view.module)) continue;
    if (!permissions.moduleEnabled(view.module) || !view.permissions.every((p) => permissions.check(p))) continue;
    modules.push(view.module);
  }
  return modules;
}
