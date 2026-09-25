import type { RealtimeEnvelope } from "./ws-manager.js";

// typescript-sdk-reference.md §7 "RealtimeMessage".
export type RealtimeMessageType = "record.created" | "record.updated" | "record.deleted" | "custom";

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

const RECORD_TYPES: ReadonlySet<string> = new Set(["record.created", "record.updated", "record.deleted"]);

function isRecordType(type: string): type is Exclude<RealtimeMessageType, "custom"> {
  return RECORD_TYPES.has(type);
}

// A record change carries { resource, record_id, record? } as its payload;
// any other envelope type is a custom message and keeps its payload whole.
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
    ...(payload.record !== undefined ? { record: payload.record } : {}),
  };
}
