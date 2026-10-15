import { PermissionContext, useAuth, usePermissionsStatus } from "@goerp/sdk/auth";
import { useViewRegistryStatus, ViewRegistryContext } from "@goerp/sdk/schema";
import { useRouterState } from "@tanstack/react-router";
import { useContext, useEffect, useRef } from "react";
import { ensureModuleRegistered } from "./module-loader.js";
import { predictPreloadModules, readModuleHistory, recordModuleVisit } from "./route-prediction.js";

const MODULE_ROUTE_PREFIX = "/_m";
const IDLE_TIMEOUT_MS = 2000;

function whenIdle(callback: () => void): () => void {
  if (typeof window.requestIdleCallback === "function") {
    const handle = window.requestIdleCallback(callback, { timeout: IDLE_TIMEOUT_MS });
    return () => window.cancelIdleCallback(handle);
  }
  const handle = window.setTimeout(callback, 0);
  return () => window.clearTimeout(handle);
}

// Records each module the user opens and, once the first route has rendered, loads the bundles
// of the modules they are most likely to open next. A failed preload is dropped silently, so the
// module's next navigation loads it lazily and reports any error there.
export function useBundlePreload(enabled: boolean): void {
  const { user } = useAuth();
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  const pathname = useRouterState({ select: (s) => s.resolvedLocation?.pathname ?? null });
  const userId = enabled && registryStatus === "ready" && permissionsStatus === "ready" ? user?.id : undefined;
  const currentModule =
    registry && pathname?.startsWith(`${MODULE_ROUTE_PREFIX}/`)
      ? (registry.resolveRoute(pathname.slice(MODULE_ROUTE_PREFIX.length))?.module ?? null)
      : null;

  useEffect(() => {
    if (userId && currentModule) recordModuleVisit(userId, currentModule);
  }, [userId, currentModule]);

  const preloadedFor = useRef<string | null>(null);
  useEffect(() => {
    if (!userId || !registry || !permissions || pathname === null || preloadedFor.current === userId) return;
    return whenIdle(() => {
      preloadedFor.current = userId;
      const modules = predictPreloadModules({
        registry,
        permissions,
        history: readModuleHistory(userId),
        exclude: currentModule,
      });
      for (const moduleName of modules) {
        ensureModuleRegistered(
          moduleName,
          registry.getBundleUrl(moduleName),
          registry.getBundleSHA256(moduleName),
        ).catch(() => {});
      }
    });
  }, [userId, registry, permissions, pathname, currentModule]);
}
