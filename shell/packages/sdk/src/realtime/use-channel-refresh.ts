import { useEffect } from "react";
import { wsManager } from "./ws-manager.js";

// Refetches via refresh whenever a message of messageType arrives on
// channel (null skips subscribing). Shared by every provider that needs a
// live WS-triggered refresh of its own fetched data — PermissionProvider's
// tenant-/user-channel effects and ViewRegistryProvider's schema.updated/
// module.installed effects both use this.
export function useChannelRefresh(
  enabled: boolean,
  channel: string | null,
  messageType: string,
  refresh: (isCancelled: () => boolean, fallbackToEmptyOnError: boolean) => void,
): void {
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
