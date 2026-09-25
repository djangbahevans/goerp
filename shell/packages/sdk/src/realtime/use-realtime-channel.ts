import { useEffect, useEffectEvent } from "react";
import { realtime } from "./realtime-api.js";
import type { RealtimeMessage } from "./realtime-message.js";

export interface UseRealtimeChannelOptions {
  // false subscribes to nothing.
  enabled?: boolean | undefined;
  filter?: ((message: RealtimeMessage) => boolean) | undefined;
}

// typescript-sdk-reference.md §7 "useRealtimeChannel". The subscription
// follows channel and enabled only; each message goes to the latest
// handler and filter, so an inline arrow doesn't resubscribe every render.
export function useRealtimeChannel(
  channel: string,
  handler: (message: RealtimeMessage) => void,
  options: UseRealtimeChannelOptions = {},
): void {
  const { enabled = true } = options;
  const { filter } = options;
  const onMessage = useEffectEvent((message: RealtimeMessage) => {
    if (filter && !filter(message)) return;
    handler(message);
  });

  useEffect(() => {
    if (!enabled) return;
    return realtime.subscribe(channel, (message) => onMessage(message));
  }, [channel, enabled]);
}
