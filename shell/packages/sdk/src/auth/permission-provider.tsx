import { createContext, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { tenantChannel, wsManager } from "../realtime/index.js";
import { fetchPermissions } from "./permission-client.js";
import type { PermissionContextValue, PermissionData } from "./permission-types.js";
import { useAuth } from "./use-auth.js";

export const PermissionContext = createContext<PermissionContextValue | null>(null);

const EMPTY_DATA: PermissionData = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };

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
  children,
}: {
  isAuthenticated: boolean;
  tenantId: string | null;
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

  // Live refresh (goerp#614): refetches on tenantChannel(tenantId)'s
  // module-install broadcast (goerp#621) rather than trusting its payload,
  // since the message only signals "something changed," not what.
  useEffect(() => {
    if (!isAuthenticated || !tenantId) return;
    let cancelled = false;
    const unsubscribe = wsManager.subscribe(tenantChannel(tenantId), (message) => {
      if (message.type === "module.installed") refresh(() => cancelled, false);
    });
    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, [isAuthenticated, tenantId, refresh]);

  const data = isAuthenticated ? loadedData : EMPTY_DATA;
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
    >
      {children}
    </PermissionProviderForUser>
  );
}
