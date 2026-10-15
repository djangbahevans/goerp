import type { QueryKey } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

// The view on screen, read by UI instructions: `reload` decides whether its
// target is showing, and `navigate` interpolates from `record`.
export interface ExecutionContext {
  // "form" or "list" for the generic renderers; null for any other page, which
  // a targeted reload never matches.
  view: "form" | "list" | null;
  resource: string | null;
  // Set for a form view of an existing record.
  recordId: string | null;
  record: Record<string, unknown> | null;
  // The view's primary query, invalidated by `reload`. Null makes even an
  // untargeted reload a no-op.
  queryKey: QueryKey | null;
}

interface ContextSlot {
  ctx: ExecutionContext;
}

const EMPTY: ExecutionContext = { view: null, resource: null, recordId: null, record: null, queryKey: null };

// One slot per mounted view; the most recently mounted is current, so closing
// a form modal over a list makes the list current again.
let slots: ContextSlot[] = [];

function mountExecutionContext(slot: ContextSlot): () => void {
  slots = [...slots, slot];
  return () => {
    slots = slots.filter((s) => s !== slot);
  };
}

export function currentExecutionContext(): ExecutionContext {
  return slots.at(-1)?.ctx ?? EMPTY;
}

// Registers the calling view's context for as long as it is mounted. Pass
// null to register nothing, e.g. for a list embedded in a form.
export function useExecutionContext(ctx: ExecutionContext | null): void {
  const slot = useRef<ContextSlot>({ ctx: ctx ?? EMPTY });
  // Written during render so an instruction arriving before this render's
  // effects already sees the new record.
  slot.current.ctx = ctx ?? EMPTY;
  const registered = ctx !== null;
  useEffect(() => (registered ? mountExecutionContext(slot.current) : undefined), [registered]);
}
