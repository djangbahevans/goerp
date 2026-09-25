import { createContext, type ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";
import { isSessionExpired } from "../auth/auth-machine.js";
import { useAuth } from "../auth/use-auth.js";
import { tenantChannel, useChannelRefresh } from "../realtime/index.js";
import { schemaRegistry } from "./schema-registry.js";
import { buildEmptyViewRegistry, buildViewRegistry, type ViewRegistry } from "./view-registry.js";

export const ViewRegistryContext = createContext<ViewRegistry | null>(null);

// Whether the current session's first schema fetch is still in flight,
// succeeded, or failed. Before it settles — and after a failure — the context
// holds the same empty registry a tenant with no modules gets, so a consumer
// deciding "nothing to show" needs this. "ready" outside a provider, where
// there's nothing to wait for.
export type LoadStatus = "loading" | "ready" | "error";

export const ViewRegistryStatusContext = createContext<LoadStatus>("ready");

export function useViewRegistryStatus(): LoadStatus {
  return useContext(ViewRegistryStatusContext);
}

const EMPTY_REGISTRY = buildEmptyViewRegistry();

// Mirrors the context value below for the one consumer that can't use
// `useContext` — TanStack Router's `beforeLoad`/`loader` run outside the
// React tree (goerp#671's `/_m/$` catch-all route), the same reason
// permission-provider.tsx exports `permissionDataRef` alongside
// `PermissionContext`.
export const viewRegistryRef: { current: ViewRegistry } = { current: EMPTY_REGISTRY };

// Mirrors permission-provider.tsx's PermissionProviderForUser/PermissionProvider
// split exactly — same shape of problem (server-derived data that needs a
// live WS-triggered refresh), same fix. `onUpdate` is how the app tells its
// router to re-run active loaders after a rebuild (`router.invalidate()`);
// kept as an injected callback rather than importing the router here, so
// this package never depends on the concrete router instance app.tsx owns.
export function ViewRegistryProviderForTenant({
  isAuthenticated,
  sessionExpired = false,
  tenantId,
  onUpdate,
  children,
}: {
  isAuthenticated: boolean;
  // Keeps the loaded registry, without refetching, while the session is
  // expired, so the page under the session-expired modal still resolves.
  sessionExpired?: boolean | undefined;
  tenantId: string | null;
  onUpdate?: (() => void) | undefined;
  children: ReactNode;
}) {
  const [registry, setRegistry] = useState<ViewRegistry>(EMPTY_REGISTRY);
  const [status, setStatus] = useState<LoadStatus>("loading");

  const latestRequestId = useRef(0);
  const refresh = useCallback((isCancelled: () => boolean, fallbackToEmptyOnError: boolean) => {
    const requestId = ++latestRequestId.current;
    // Bypasses schemaRegistry's own indefinite cache — a hot-reloaded
    // schema is exactly the case that cache doesn't know how to
    // invalidate on its own (schema-registry.ts's own doc comment notes
    // this is "the full view registry's concern").
    schemaRegistry.invalidate();
    void schemaRegistry
      .getSchema()
      .then((schema) => {
        if (isCancelled() || latestRequestId.current !== requestId) return;
        setRegistry(buildViewRegistry(schema));
        setStatus("ready");
      })
      .catch(() => {
        if (!isCancelled() && fallbackToEmptyOnError && latestRequestId.current === requestId) {
          setRegistry(EMPTY_REGISTRY);
          setStatus("error");
        }
      });
  }, []);

  useEffect(() => {
    if (!isAuthenticated) return;
    let cancelled = false;
    refresh(() => cancelled, true);
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, refresh]);

  // schema.updated (goerp#671/djangbahevans/goerp#802): a module hot-reload
  // completed. module.installed: a newly installed module also changes the
  // schema — its views/routes/navigation weren't in any prior fetch.
  useChannelRefresh(isAuthenticated, tenantId && tenantChannel(tenantId), "schema.updated", refresh);
  useChannelRefresh(isAuthenticated, tenantId && tenantChannel(tenantId), "module.installed", refresh);

  const hasSession = isAuthenticated || sessionExpired;
  const value = hasSession ? registry : EMPTY_REGISTRY;
  // Keeps viewRegistryRef (and the router, via onUpdate) in lockstep with
  // `value` for every transition — same reasoning as
  // permission-provider.tsx's identical effect over permissionDataRef.
  useEffect(() => {
    viewRegistryRef.current = value;
    onUpdate?.();
  }, [value, onUpdate]);

  return (
    <ViewRegistryContext.Provider value={value}>
      <ViewRegistryStatusContext.Provider value={hasSession ? status : "loading"}>
        {children}
      </ViewRegistryStatusContext.Provider>
    </ViewRegistryContext.Provider>
  );
}

// Keyed by tenant id so switching tenants on the same tab remounts with a
// fresh EMPTY_REGISTRY instead of exposing the previous tenant's schema
// until the new fetch resolves — same convention as PermissionProvider's
// key={user?.id}.
export function ViewRegistryProvider({
  onUpdate,
  children,
}: {
  onUpdate?: (() => void) | undefined;
  children: ReactNode;
}) {
  const { isAuthenticated, state, tenant } = useAuth();
  return (
    <ViewRegistryProviderForTenant
      key={tenant?.id ?? "anonymous"}
      isAuthenticated={isAuthenticated}
      sessionExpired={isSessionExpired(state)}
      tenantId={tenant?.id ?? null}
      onUpdate={onUpdate}
    >
      {children}
    </ViewRegistryProviderForTenant>
  );
}
