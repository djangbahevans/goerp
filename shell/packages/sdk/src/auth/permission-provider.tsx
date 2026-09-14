import { createContext, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { tenantChannel, useChannelRefresh, userChannel } from "../realtime/index.js";
import { fetchPermissions } from "./permission-client.js";
import type { PermissionContextValue, PermissionData } from "./permission-types.js";
import { useAuth } from "./use-auth.js";

export const PermissionContext = createContext<PermissionContextValue | null>(null);

const EMPTY_DATA: PermissionData = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };

// Mirrors `data`/`value` below for the one consumer that can't use
// `useContext` — TanStack Router's `beforeLoad`/`loader` run outside the
// React tree entirely (goerp#671's `/_m/$` catch-all route). Kept in sync
// by the same `refresh` that calls `setLoadedData` below, so it's never
// more than one WS round trip behind what `PermissionContext` itself
// reports.
export const permissionDataRef: { current: PermissionData } = { current: EMPTY_DATA };

// `resourceId` is accepted for call-shape parity with typescript-sdk-reference.md but not evaluated:
// /_meta/permissions carries no per-record ABAC data — real resource-level ABAC is enforced server-side
// (host.authz.check) on the request the gated action actually makes.
export function createPermissionContextValue(data: PermissionData): PermissionContextValue {
  return {
    ...data,
    check: (permission) => data.permissions.has(permission),
    checkField: (model, field, mode) => {
      const access = data.fieldAccess[model]?.[field];
      if (!access) return false;
      return mode === "read" ? access.read : access.write;
    },
    moduleEnabled: (moduleName) => data.modulesEnabled.has(moduleName),
  };
}

// Exported for testing — lets tests drive isAuthenticated/tenantId directly
// instead of standing up a full AuthProvider/auth machine.
export function PermissionProviderForUser({
  isAuthenticated,
  tenantId,
  userId,
  children,
}: {
  isAuthenticated: boolean;
  tenantId: string | null;
  userId: string | null;
  children: ReactNode;
}) {
  const [loadedData, setLoadedData] = useState<PermissionData>(EMPTY_DATA);

  // Shared by both effects below so a fetch triggered by one can never be
  // clobbered by a slower, already-superseded fetch from the other landing
  // later — only the response to the most recently issued request is ever
  // applied.
  const latestRequestId = useRef(0);
  const refresh = useCallback((isCancelled: () => boolean, fallbackToEmptyOnError: boolean) => {
    const requestId = ++latestRequestId.current;
    void fetchPermissions()
      .then((result) => {
        if (!isCancelled() && latestRequestId.current === requestId) setLoadedData(result);
      })
      .catch(() => {
        if (!isCancelled() && fallbackToEmptyOnError && latestRequestId.current === requestId)
          setLoadedData(EMPTY_DATA);
      });
  }, []);

  useEffect(() => {
    if (!isAuthenticated) return;
    let cancelled = false;
    // Deny-by-default on failure: check()/checkField() must still return a boolean.
    refresh(() => cancelled, true);
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, refresh]);

  // Live refresh on module install (goerp#614/#621), role change (goerp#624/#619),
  // and tenant plan change (goerp#629/#628).
  useChannelRefresh(isAuthenticated, tenantId && tenantChannel(tenantId), "module.installed", refresh);
  useChannelRefresh(isAuthenticated, tenantId && tenantChannel(tenantId), "plan.changed", refresh);
  useChannelRefresh(isAuthenticated, userId && userChannel(userId), "role.changed", refresh);

  const data = isAuthenticated ? loadedData : EMPTY_DATA;
  // Keeps permissionDataRef in lockstep with `data` for every transition
  // (login, logout, refetch, account switch via the key= remount above) —
  // a single effect here is simpler and harder to get wrong than mirroring
  // the assignment at every place `data` can change.
  useEffect(() => {
    permissionDataRef.current = data;
  }, [data]);
  const value = useMemo(() => createPermissionContextValue(data), [data]);
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

// Keyed by user id so switching accounts on the same tab remounts with a fresh EMPTY_DATA instead of
// exposing the previous user's permissions until the new fetch resolves.
export function PermissionProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated, user, tenant } = useAuth();
  return (
    <PermissionProviderForUser
      key={user?.id ?? "anonymous"}
      isAuthenticated={isAuthenticated}
      tenantId={tenant?.id ?? null}
      userId={user?.id ?? null}
    >
      {children}
    </PermissionProviderForUser>
  );
}
