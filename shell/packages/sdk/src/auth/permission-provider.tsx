import { createContext, type ReactNode, useEffect, useMemo, useState } from "react";
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

function PermissionProviderForUser({ isAuthenticated, children }: { isAuthenticated: boolean; children: ReactNode }) {
  const [loadedData, setLoadedData] = useState<PermissionData>(EMPTY_DATA);

  useEffect(() => {
    if (!isAuthenticated) return;

    let cancelled = false;
    void fetchPermissions()
      .then((result) => {
        if (!cancelled) setLoadedData(result);
      })
      .catch(() => {
        // Deny-by-default: check()/checkField() must still return a boolean.
        if (!cancelled) setLoadedData(EMPTY_DATA);
      });
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated]);

  const data = isAuthenticated ? loadedData : EMPTY_DATA;
  const value = useMemo(() => createPermissionContextValue(data), [data]);
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

// Keyed by user id so switching accounts on the same tab remounts with a fresh EMPTY_DATA instead of
// exposing the previous user's permissions until the new fetch resolves.
export function PermissionProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated, user } = useAuth();
  return (
    <PermissionProviderForUser key={user?.id ?? "anonymous"} isAuthenticated={isAuthenticated}>
      {children}
    </PermissionProviderForUser>
  );
}
