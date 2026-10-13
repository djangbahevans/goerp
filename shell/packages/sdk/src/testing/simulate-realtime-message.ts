import type { RealtimeMessage } from "../realtime/realtime-message.js";
import { wsManager } from "../realtime/ws-manager.js";

export type SimulatedRealtimeMessage = Omit<RealtimeMessage, "event"> & { event?: string };

export function simulateRealtimeMessage(channel: string, message: SimulatedRealtimeMessage): void {
  wsManager.dispatch({
    channel,
    type: message.type === "custom" ? (message.event ?? "custom") : message.type,
    payload:
      message.type === "custom"
        ? message.payload
        : { resource: message.resource, record_id: message.recordId, record: message.record },
  });
}
