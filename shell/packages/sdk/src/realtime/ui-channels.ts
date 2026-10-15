import { useEffect } from "react";
import { executeInstructions, type UIInstruction } from "./ui-executor.js";
import { type RealtimeEnvelope, recordUIChannel, wsManager } from "./ws-manager.js";

export const UI_PUSH_MESSAGE = "ui.push";

// Runs a `ui.push` message's instructions as a push, so only toast, reload and
// set_badge execute. Other message types on the channel are left to their own
// subscribers.
export function handleUIPush(message: RealtimeEnvelope): void {
  if (message.type !== UI_PUSH_MESSAGE) return;
  const instructions = (message.payload as { instructions?: unknown } | null | undefined)?.instructions;
  if (!Array.isArray(instructions)) {
    console.warn(`ui: ui.push on ${message.channel} has no instructions array; skipped`);
    return;
  }
  void executeInstructions(instructions as UIInstruction[], "push");
}

// Subscribes to a record's `ui:record` channel while the record is shown.
// An undefined recordId (a new-record form) subscribes to nothing.
export function useRecordUIChannel(resource: string, recordId: string | undefined): void {
  useEffect(() => {
    if (!resource || !recordId) return;
    return wsManager.subscribe(recordUIChannel(resource, recordId), handleUIPush);
  }, [resource, recordId]);
}
