import { useAuth } from "@goerp/sdk/auth";
import { handleUIPush, roleChannel, userChannel, wsManager } from "@goerp/sdk/realtime";
import { useEffect } from "react";

const ROLE_CHANGED_MESSAGE = "role.changed";

// Subscribes the signed-in user to `ui:user:{id}` and `ui:role:{role}` for each
// role. A role grant or revocation arrives as role.changed on the user channel;
// reloading the session picks up the live roles, which moves the role
// subscriptions without a page reload.
export function useUIChannels(): void {
  const { isAuthenticated, user, reloadSession } = useAuth();
  const userId = isAuthenticated ? user?.id : undefined;
  const roles = JSON.stringify(isAuthenticated ? [...new Set(user?.roles)].sort() : []);

  useEffect(() => {
    if (!userId) return;
    return wsManager.subscribe(userChannel(userId), (message) => {
      if (message.type === ROLE_CHANGED_MESSAGE) {
        reloadSession().catch((err: unknown) => console.warn("ui: reloading roles after role.changed failed", err));
        return;
      }
      handleUIPush(message);
    });
  }, [userId, reloadSession]);

  useEffect(() => {
    if (!userId) return;
    const unsubscribers = (JSON.parse(roles) as string[]).map((role) =>
      wsManager.subscribe(roleChannel(role), handleUIPush),
    );
    return () => {
      for (const unsubscribe of unsubscribers) unsubscribe();
    };
  }, [userId, roles]);
}
