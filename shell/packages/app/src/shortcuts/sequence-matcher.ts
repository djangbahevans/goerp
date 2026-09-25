import { matchesStep, type Shortcut } from "./shortcut.js";

export interface ShortcutBinding {
  shortcut: Shortcut;
  run: () => void;
}

// shell-ux.md §7.3: each step of a sequence must follow the previous one
// within this window.
export const SEQUENCE_TIMEOUT_MS = 1500;

interface Pending {
  candidates: ShortcutBinding[];
  depth: number;
  at: number;
}

export interface SequenceMatcher {
  // The binding the keystroke completes, if any.
  handle(event: KeyboardEvent, bindings: ShortcutBinding[], now: number): ShortcutBinding | undefined;
  reset(): void;
}

export function createSequenceMatcher(mac: boolean): SequenceMatcher {
  let pending: Pending | null = null;

  const advance = (candidates: ShortcutBinding[], depth: number, event: KeyboardEvent) =>
    candidates.filter((binding) => {
      const step = binding.shortcut[depth];
      return step !== undefined && matchesStep(step, event, mac);
    });

  return {
    handle(event, bindings, now) {
      let depth = 0;
      let matched: ShortcutBinding[] = [];
      if (pending && now - pending.at <= SEQUENCE_TIMEOUT_MS) {
        depth = pending.depth;
        matched = advance(pending.candidates, depth, event);
      }
      // A key that breaks a sequence may still start a new one ("G G H").
      if (matched.length === 0) {
        depth = 0;
        matched = advance(bindings, 0, event);
      }
      pending = null;
      const completed = matched.find((binding) => binding.shortcut.length === depth + 1);
      if (completed) return completed;
      if (matched.length > 0) pending = { candidates: matched, depth: depth + 1, at: now };
      return undefined;
    },
    reset() {
      pending = null;
    },
  };
}
