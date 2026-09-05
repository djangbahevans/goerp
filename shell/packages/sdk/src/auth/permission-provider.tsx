import { createContext, type ReactNode, useEffect, useState } from "react";
import { fetchPermissions } from "./permission-client.js";
import type { PermissionContextValue, PermissionData } from "./permission-types.js";
import { useAuth } from "./use-auth.js";

export const PermissionContext = createContext<PermissionContextValue | null>(null);

const EMPTY_DATA: PermissionData = { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() };

// createPermissionContextValue builds the synchronous checks Can/
// usePermission/useFieldPermission read from. `resourceId` is accepted by
// `check` for parity with typescript-sdk-reference.md's documented
// usePermission(permission, resourceId) call shape, but doesn't affect the
// result: GET /_meta/permissions carries no per-record ABAC data to
// evaluate it against, and real resource-level ABAC is enforced
// server-side (host.authz.check, auth-internals.md §13) on the request the
// gated action actually makes — this check is a UI-only optimization, not
// the security boundary.
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

export function PermissionProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const [loadedData, setLoadedData] = useState<PermissionData>(EMPTY_DATA);

  useEffect(() => {
    if (!isAuthenticated) return;

    let cancelled = false;
    void fetchPermissions()
      .then((result) => {
        if (!cancelled) setLoadedData(result);
      })
      .catch(() => {
        // Deny-by-default: no page-level error slot for this fetch exists
        // yet, and check()/checkField() must still return a boolean.
        if (!cancelled) setLoadedData(EMPTY_DATA);
      });
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated]);

  // Derived rather than reset from the effect above: an unauthenticated
  // session reads as empty immediately, with no synchronous setState
  // inside the effect.
  const data = isAuthenticated ? loadedData : EMPTY_DATA;
  const value = createPermissionContextValue(data);
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}
