import { describe, expect, it } from "vitest";
import {
  describeShortcut,
  formatShortcutText,
  formatStep,
  matchesStep,
  parseShortcut,
  type Shortcut,
  type ShortcutStep,
  shortcutsConflict,
} from "./shortcut.js";

// Unit tests run without a DOM, so a keystroke is the plain fields read.
function key(init: Partial<KeyboardEvent>): KeyboardEvent {
  return { code: "", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...init } as KeyboardEvent;
}

function step(text: string): ShortcutStep {
  const parsed = parseShortcut(text);
  if (!parsed?.[0]) throw new Error(`bad test shortcut ${text}`);
  return parsed[0];
}

function shortcut(text: string): Shortcut {
  const parsed = parseShortcut(text);
  if (!parsed) throw new Error(`bad test shortcut ${text}`);
  return parsed;
}

describe("parseShortcut", () => {
  it("parses a sequence into one step per key", () => {
    expect(parseShortcut("N C")).toEqual([
      { key: "n", mod: false, ctrl: false, alt: false, shift: false },
      { key: "c", mod: false, ctrl: false, alt: false, shift: false },
    ]);
  });

  it("parses a chord's modifiers case-insensitively", () => {
    expect(parseShortcut("mod+Shift+P")).toEqual([{ key: "p", mod: true, ctrl: false, alt: false, shift: true }]);
  });

  it("treats a trailing + as the key", () => {
    expect(parseShortcut("Mod++")).toEqual([{ key: "+", mod: true, ctrl: false, alt: false, shift: false }]);
  });

  it("rejects an unknown modifier, an empty string and a missing key", () => {
    expect(parseShortcut("Hyper+K")).toBeNull();
    expect(parseShortcut("  ")).toBeNull();
    expect(parseShortcut("Mod+")).toBeNull();
  });
});

describe("matchesStep", () => {
  it("matches Mod to Ctrl off a Mac and Cmd on a Mac", () => {
    expect(matchesStep(step("Mod+K"), key({ key: "k", ctrlKey: true }), false)).toBe(true);
    expect(matchesStep(step("Mod+K"), key({ key: "k", metaKey: true }), false)).toBe(false);
    expect(matchesStep(step("Mod+K"), key({ key: "k", metaKey: true }), true)).toBe(true);
    expect(matchesStep(step("Mod+K"), key({ key: "k", ctrlKey: true }), true)).toBe(false);
  });

  it("requires the exact modifier set", () => {
    expect(matchesStep(step("Mod+K"), key({ key: "k", ctrlKey: true, altKey: true }), false)).toBe(false);
    expect(matchesStep(step("G"), key({ key: "g", ctrlKey: true }), false)).toBe(false);
  });

  it("matches a letter only with Shift up unless the step names Shift", () => {
    expect(matchesStep(step("G"), key({ key: "g" }), false)).toBe(true);
    expect(matchesStep(step("G"), key({ key: "G", shiftKey: true }), false)).toBe(false);
    expect(matchesStep(step("Shift+G"), key({ key: "G", shiftKey: true }), false)).toBe(true);
  });

  it("ignores Shift for a symbol, so ? matches however the layout produces it", () => {
    expect(matchesStep(step("?"), key({ key: "?", shiftKey: true }), false)).toBe(true);
    expect(matchesStep(step("?"), key({ key: "?" }), false)).toBe(true);
  });

  it("matches a letter by its key code when Alt changes the character", () => {
    expect(matchesStep(step("Alt+N"), key({ key: "˜", code: "KeyN", altKey: true }), true)).toBe(true);
  });

  it("matches a named key case-insensitively", () => {
    expect(matchesStep(step("Escape"), key({ key: "Escape" }), false)).toBe(true);
  });
});

describe("shortcutsConflict", () => {
  it("treats equal shortcuts as conflicting", () => {
    expect(shortcutsConflict(shortcut("G H"), shortcut("g h"), false)).toBe(true);
  });

  it("treats a shortcut that starts another's sequence as conflicting, both ways", () => {
    expect(shortcutsConflict(shortcut("G"), shortcut("G H"), false)).toBe(true);
    expect(shortcutsConflict(shortcut("G H X"), shortcut("G H"), false)).toBe(true);
  });

  it("lets sequences that only share a first step coexist", () => {
    expect(shortcutsConflict(shortcut("G X"), shortcut("G H"), false)).toBe(false);
  });

  it("compares Mod and Ctrl as the physical chord they are on each platform", () => {
    expect(shortcutsConflict(shortcut("Ctrl+K"), shortcut("Mod+K"), false)).toBe(true);
    expect(shortcutsConflict(shortcut("Ctrl+K"), shortcut("Mod+K"), true)).toBe(false);
  });
});

describe("formatting", () => {
  it("uses Mac glyphs in ⌃⌥⇧⌘ order with no separator", () => {
    expect(formatStep(step("Mod+Shift+Alt+Ctrl+P"), true)).toBe("⌃⌥⇧⌘P");
  });

  it("spells out modifiers elsewhere", () => {
    expect(formatStep(step("Mod+Shift+P"), false)).toBe("Ctrl Shift P");
    expect(formatStep(step("Escape"), false)).toBe("Esc");
  });

  it("formats a raw shortcut string, leaving unparseable text as is", () => {
    expect(formatShortcutText("N C", false)).toBe("N C");
    expect(formatShortcutText("Mod+/", true)).toBe("⌘/");
    expect(formatShortcutText("Hyper+K", false)).toBe("Hyper+K");
  });

  it("describes a shortcut for a screen reader", () => {
    expect(describeShortcut(shortcut("Mod+/"), true)).toBe("Command slash");
    expect(describeShortcut(shortcut("Mod+/"), false)).toBe("Control slash");
    expect(describeShortcut(shortcut("G H"), false)).toBe("G then H");
    expect(describeShortcut(shortcut("?"), false)).toBe("question mark");
  });
});
