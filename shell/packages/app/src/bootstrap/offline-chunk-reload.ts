import type { OnlineState } from "@goerp/sdk/realtime";

// The key TanStack Router's lazyRouteComponent sets before reloading once for a route chunk that
// failed to load, so it doesn't reload again for the same failure.
const RELOAD_KEY_PREFIX = "tanstack_router_reload:";

// That reload picks up a new deployment when a chunk has gone missing. Offline it replaces the shell
// with the browser's own offline page, so while offline the reload is reported as already done and
// the route's error boundary shows the offline message in place. Returns a function that restores
// getItem.
export function suppressChunkReloadWhileOffline(storage: Storage, online: Pick<OnlineState, "get">): () => void {
  const getItem = Storage.prototype.getItem;
  Storage.prototype.getItem = function (this: Storage, key: string): string | null {
    if (this === storage && key.startsWith(RELOAD_KEY_PREFIX) && !online.get()) return "1";
    return getItem.call(this, key);
  };
  return () => {
    Storage.prototype.getItem = getItem;
  };
}
