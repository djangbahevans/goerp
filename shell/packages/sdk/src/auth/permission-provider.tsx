import { createContext, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { tenantChannel, userChannel, wsManager } from "../realtime/index.js";
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

// Refetches via refresh whenever a message of messageType arrives on
// channel (null skips subscribing). Shared by the tenant- and user-channel
// live-refresh effects below, which differ only in channel/messageType.
function useChannelRefresh(
  enabled: boolean,
  channel: string | null,
  messageType: string,
  refresh: (isCancelled: () => boolean, fallbackToEmptyOnError: boolean) => void,
) {
  useEffect(() => {
    if (!enabled || !channel) return;
    let cancelled = false;
    const unsubscribe = wsManager.subscribe(channel, (message) => {
      if (message.type === messageType) refresh(() => cancelled, false);
    });
    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, [enabled, channel, messageType, refresh]);
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

  // Live refresh on module install (goerp#614/#621) and role change (goerp#624/#619).
  useChannelRefresh(isAuthenticated, tenantId && tenantChannel(tenantId), "module.installed", refresh);
  useChannelRefresh(isAuthenticated, userId && userChannel(userId), "role.changed", refresh);

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
      userId={user?.id ?? null}
    >
      {children}
    </PermissionProviderForUser>
  );
}
