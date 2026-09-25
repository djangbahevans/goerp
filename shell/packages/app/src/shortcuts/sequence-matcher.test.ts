import { describe, expect, it, vi } from "vitest";
import { createSequenceMatcher, SEQUENCE_TIMEOUT_MS, type ShortcutBinding } from "./sequence-matcher.js";
import { parseShortcut } from "./shortcut.js";

function binding(text: string): ShortcutBinding {
  const shortcut = parseShortcut(text);
  if (!shortcut) throw new Error(`bad test shortcut ${text}`);
  return { shortcut, run: vi.fn() };
}

// Unit tests run without a DOM, so a keystroke is the plain fields read.
function press(key: string, init: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return { key, code: "", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...init } as KeyboardEvent;
}

describe("createSequenceMatcher", () => {
  const goHome = binding("G H");
  const goSettings = binding("G S");
  const palette = binding("Mod+K");
  const help = binding("?");
  const bindings = [goHome, goSettings, palette, help];

  it("completes a chord in one keystroke", () => {
    const matcher = createSequenceMatcher(false);
    expect(matcher.handle(press("k", { ctrlKey: true }), bindings, 0)).toBe(palette);
  });

  it("completes a single-key shortcut", () => {
    const matcher = createSequenceMatcher(false);
    expect(matcher.handle(press("?", { shiftKey: true }), bindings, 0)).toBe(help);
  });

  it("completes a sequence on its last step, picking the matching branch", () => {
    const matcher = createSequenceMatcher(false);
    expect(matcher.handle(press("g"), bindings, 0)).toBeUndefined();
    expect(matcher.handle(press("s"), bindings, 100)).toBe(goSettings);
  });

  it("drops a sequence whose next step comes after the timeout", () => {
    const matcher = createSequenceMatcher(false);
    matcher.handle(press("g"), bindings, 0);
    expect(matcher.handle(press("h"), bindings, SEQUENCE_TIMEOUT_MS + 1)).toBeUndefined();
  });

  it("starts over on a key that breaks the sequence", () => {
    const matcher = createSequenceMatcher(false);
    matcher.handle(press("g"), bindings, 0);
    expect(matcher.handle(press("x"), bindings, 10)).toBeUndefined();
    expect(matcher.handle(press("h"), bindings, 20)).toBeUndefined();
  });

  it("lets a breaking key start a new sequence", () => {
    const matcher = createSequenceMatcher(false);
    matcher.handle(press("g"), bindings, 0);
    expect(matcher.handle(press("g"), bindings, 10)).toBeUndefined();
    expect(matcher.handle(press("h"), bindings, 20)).toBe(goHome);
  });

  it("forgets a pending sequence on reset", () => {
    const matcher = createSequenceMatcher(false);
    matcher.handle(press("g"), bindings, 0);
    matcher.reset();
    expect(matcher.handle(press("h"), bindings, 10)).toBeUndefined();
  });
});
