import { type RealtimeMessage, toRealtimeMessage } from "./realtime-message.js";
import { type WebSocketManager, wsManager } from "./ws-manager.js";

export type RealtimeHandler = (message: RealtimeMessage) => void;

export interface RealtimeSubscribeOptions {
  filter?: ((message: RealtimeMessage) => boolean) | undefined;
}

// typescript-sdk-reference.md §7 "RealtimeAPI": ModuleContext.realtime, the
// same subscription useRealtimeChannel makes, for code outside React.
export interface RealtimeAPI {
  subscribe(channel: string, handler: RealtimeHandler, options?: RealtimeSubscribeOptions): () => void;
}

export function createRealtimeAPI(manager: Pick<WebSocketManager, "subscribe">): RealtimeAPI {
  return {
    subscribe(channel, handler, options = {}) {
      const { filter } = options;
      return manager.subscribe(channel, (envelope) => {
        const message = toRealtimeMessage(envelope);
        if (filter && !filter(message)) return;
        handler(message);
      });
    },
  };
}

export const realtime: RealtimeAPI = createRealtimeAPI(wsManager);
