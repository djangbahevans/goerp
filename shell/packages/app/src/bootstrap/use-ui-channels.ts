import { useAuth } from "@goerp/sdk/auth";
import { subscribeNotifications } from "@goerp/sdk/notifications";
import { handleUIPush, roleChannel, userChannel, wsManager } from "@goerp/sdk/realtime";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";

const ROLE_CHANGED_MESSAGE = "role.changed";

// Role subscriptions follow user.roles, which a session reload refreshes from
// the live grants: on role.changed, and on reconnect for changes missed meanwhile.
export function useUIChannels(): void {
  const { isAuthenticated, user, reloadSession } = useAuth();
  const userId = isAuthenticated && !user?.passwordChangeRequired ? user?.id : undefined;
  const roleKey = userId ? [...new Set(user?.roles)].sort().join("\n") : "";
  const roles = useMemo(() => (roleKey ? roleKey.split("\n") : []), [roleKey]);
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!userId) return;
    const reloadRoles = () =>
      reloadSession().catch((err: unknown) => console.warn("ui: reloading the session's roles failed", err));
    const unsubscribeUser = wsManager.subscribe(userChannel(userId), (message) => {
      if (message.type === ROLE_CHANGED_MESSAGE) void reloadRoles();
      else handleUIPush(message);
    });
    const unsubscribeReconnect = wsManager.subscribeReconnect(() => void reloadRoles());
    return () => {
      unsubscribeUser();
      unsubscribeReconnect();
    };
  }, [userId, reloadSession]);

  useEffect(() => {
    if (!userId) return;
    return subscribeNotifications(queryClient);
  }, [userId, queryClient]);

  useEffect(() => {
    if (!userId) return;
    const unsubscribers = roles.map((role) => wsManager.subscribe(roleChannel(role), handleUIPush));
    return () => {
      for (const unsubscribe of unsubscribers) unsubscribe();
    };
  }, [userId, roles]);
}
