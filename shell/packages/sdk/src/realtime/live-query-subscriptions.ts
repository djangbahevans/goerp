import type { Query, QueryClient } from "@tanstack/react-query";
import { type RealtimeEnvelope, type WebSocketManager, wsManager } from "./ws-manager.js";

type QueryPredicate = (query: Query) => boolean;

interface Registration {
  resource: string;
  matches: QueryPredicate;
}

interface ChannelQueries {
  registrations: Set<Registration>;
  unsubscribe: () => void;
}

interface ClientQueries {
  channels: Map<string, ChannelQueries>;
  unsubscribeReconnect: () => void;
}

const managers = new WeakMap<WebSocketManager, Map<QueryClient, ClientQueries>>();

function changeType(channel: string): string {
  return channel.startsWith("record:") ? "record.updated" : "records.changed";
}

function changedResource(channel: string, message: RealtimeEnvelope): string | undefined {
  if (message.type !== changeType(channel) || typeof message.payload !== "object" || message.payload === null) {
    return undefined;
  }
  const resource = (message.payload as { resource?: unknown }).resource;
  return typeof resource === "string" ? resource : undefined;
}

// A single invalidation per client prevents shared observers from canceling
// and restarting one another's refetches.
export function subscribeLiveQueries(
  queryClient: QueryClient,
  channel: string,
  resource: string,
  matches: QueryPredicate,
  manager: WebSocketManager = wsManager,
): () => void {
  let clients = managers.get(manager);
  if (!clients) {
    clients = new Map();
    managers.set(manager, clients);
  }
  let client = clients.get(queryClient);
  if (!client) {
    const channels = new Map<string, ChannelQueries>();
    client = {
      channels,
      unsubscribeReconnect: manager.subscribeReconnect((activeChannels) => {
        const registrations = [...channels]
          .filter(([name]) => activeChannels.has(name))
          .flatMap(([, queries]) => [...queries.registrations]);
        if (registrations.length === 0) return;
        void queryClient.invalidateQueries({
          predicate: (query) => registrations.some((entry) => entry.matches(query)),
        });
      }),
    };
    clients.set(queryClient, client);
  }
  let queries = client.channels.get(channel);
  if (!queries) {
    const registrations = new Set<Registration>();
    queries = {
      registrations,
      unsubscribe: manager.subscribe(channel, (message) => {
        const changed = changedResource(channel, message);
        if (changed === undefined) return;
        const matching = [...registrations].filter((entry) => entry.resource === changed);
        if (matching.length === 0) return;
        void queryClient.invalidateQueries({
          predicate: (query) => matching.some((entry) => entry.matches(query)),
        });
      }),
    };
    client.channels.set(channel, queries);
  }
  const registration = { resource, matches };
  queries.registrations.add(registration);

  return () => {
    if (!queries.registrations.delete(registration)) return;
    if (queries.registrations.size === 0) {
      queries.unsubscribe();
      client.channels.delete(channel);
    }
    if (client.channels.size === 0) {
      client.unsubscribeReconnect();
      clients.delete(queryClient);
    }
  };
}
