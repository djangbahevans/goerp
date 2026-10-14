import type { RecordOf, ResourceName } from "../names.js";
import type { RealtimeEnvelope } from "./ws-manager.js";

export type RealtimeMessageType =
  | "record.created"
  | "record.updated"
  | "record.deleted"
  | "record.unavailable"
  | "custom";

export interface RealtimeMessage {
  type: RealtimeMessageType;
  // The envelope's own type as the engine sent it, which is what tells one
  // custom message from another ("notification.new", "role.changed", ...).
  event: string;
  resource?: string;
  recordId?: string;
  record?: unknown;
  payload?: unknown;
}

const RECORD_TYPES: ReadonlySet<string> = new Set([
  "record.created",
  "record.updated",
  "record.deleted",
  "record.unavailable",
]);

function isRecordType(type: string): type is Exclude<RealtimeMessageType, "custom"> {
  return RECORD_TYPES.has(type);
}

export function toRealtimeMessage(envelope: RealtimeEnvelope): RealtimeMessage {
  if (!isRecordType(envelope.type)) {
    return { type: "custom", event: envelope.type, payload: envelope.payload };
  }
  const payload = (typeof envelope.payload === "object" && envelope.payload !== null ? envelope.payload : {}) as {
    resource?: unknown;
    record_id?: unknown;
    record?: unknown;
  };
  return {
    type: envelope.type,
    event: envelope.type,
    ...(typeof payload.resource === "string" ? { resource: payload.resource } : {}),
    ...(typeof payload.record_id === "string" ? { recordId: payload.record_id } : {}),
    ...(envelope.type !== "record.unavailable" && payload.record !== undefined ? { record: payload.record } : {}),
  };
}

export interface RecordChange<R extends ResourceName> extends RealtimeMessage {
  type: Exclude<RealtimeMessageType, "custom">;
  resource: R;
  recordId: string;
  record?: RecordOf<R>;
}

export function isRecordChange<R extends ResourceName>(
  message: RealtimeMessage,
  resource: R,
): message is RecordChange<R> {
  return isRecordType(message.type) && message.resource === resource && typeof message.recordId === "string";
}
