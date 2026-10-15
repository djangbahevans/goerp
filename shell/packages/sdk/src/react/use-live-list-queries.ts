import { type Query, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { subscribeLiveQueries } from "../realtime/live-query-subscriptions.js";
import { resourceRegistry } from "../schema/index.js";

// Invalidates the resource's queries that `matches` selects when a records.changed message for the
// resource arrives on `channel`, or on the owning module's feed when `channel` is undefined.
// `matches` must not change identity between renders.
export function useLiveListQueries(
  resource: string,
  matches: (query: Query, resource: string) => boolean,
  { channel, enabled = true }: { channel?: string | null | undefined; enabled?: boolean } = {},
): void {
  const queryClient = useQueryClient();
  useEffect(() => {
    if (!enabled || channel === null) return;
    const predicate = (query: Query) => matches(query, resource);
    if (channel !== undefined) return subscribeLiveQueries(queryClient, channel, resource, predicate);
    let cancelled = false;
    let unsubscribe: (() => void) | undefined;
    void resourceRegistry.resolve(resource).then(
      (entry) => {
        if (cancelled) return;
        unsubscribe = subscribeLiveQueries(queryClient, `feed:${entry.module}`, resource, predicate);
      },
      () => {},
    );
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [queryClient, resource, matches, channel, enabled]);
}
