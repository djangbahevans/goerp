import type { ViewRegistry } from "@goerp/sdk/schema";
import { useViewRegistryStatus, ViewRegistryContext } from "@goerp/sdk/schema";
import { useQueryClient } from "@tanstack/react-query";
import { useContext, useEffect, useRef } from "react";
import { ensureModuleRegistered, registeredBundles } from "./module-loader.js";

// The resources behind routes that were added, removed or whose view declaration changed.
export function changedRouteResources(
  before: Pick<ViewRegistry, "routes">,
  after: Pick<ViewRegistry, "routes">,
): Set<string> {
  const resources = new Set<string>();
  for (const path of new Set([...before.routes.keys(), ...after.routes.keys()])) {
    const previous = before.routes.get(path);
    const next = after.routes.get(path);
    if (previous && next && JSON.stringify(previous.declaration) === JSON.stringify(next.declaration)) continue;
    for (const view of [previous, next]) {
      if (view?.declaration.resource) resources.add(view.declaration.resource);
    }
  }
  return resources;
}

// Applies a rebuilt view registry to what the session already loaded: modules whose bundle
// changed are reloaded (disposing the old bundle first), and queries for resources whose routes
// changed are invalidated. The current route's view re-renders through its own loader.
export function useModuleHotReload(): void {
  const registry = useContext(ViewRegistryContext);
  const status = useViewRegistryStatus();
  const queryClient = useQueryClient();
  const previous = useRef<ViewRegistry | null>(null);

  useEffect(() => {
    if (!registry || status !== "ready") return;
    const before = previous.current;
    previous.current = registry;
    if (!before || before === registry) return;

    for (const [moduleName, bundle] of registeredBundles()) {
      const bundleUrl = registry.getBundleUrl(moduleName);
      const bundleSha256 = registry.getBundleSHA256(moduleName);
      if (bundleUrl === bundle.bundleUrl && bundleSha256 === bundle.bundleSha256) continue;
      // A failed load is retried, and reported, by the module's next navigation.
      ensureModuleRegistered(moduleName, bundleUrl, bundleSha256).catch(() => {});
    }

    const resources = changedRouteResources(before, registry);
    if (resources.size === 0) return;
    void queryClient.invalidateQueries({
      predicate: (query) => query.queryKey.some((part) => typeof part === "string" && resources.has(part)),
    });
  }, [registry, status, queryClient]);
}
