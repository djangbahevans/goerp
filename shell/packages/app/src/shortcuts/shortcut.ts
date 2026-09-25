// typescript-sdk-reference.md "Command shortcut format": space-separated
// steps, each a key with optional `+`-joined Mod/Ctrl/Alt/Shift modifiers.
export interface ShortcutStep {
  key: string;
  mod: boolean;
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
}

export type Shortcut = ShortcutStep[];

function isMacPlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  // || not ?? — navigator.platform is a string, not nullable, so an empty
  // string (privacy-hardened browsers) needs the same fallthrough a
  // missing value would get.
  const platform = navigator.platform || navigator.userAgent || "";
  return /Mac|iPod|iPhone|iPad/.test(platform);
}

// Computed once per module load — the platform doesn't change mid-session.
export const IS_MAC = isMacPlatform();

const MODIFIERS = new Set(["mod", "ctrl", "alt", "shift"]);

function parseStep(text: string): ShortcutStep | null {
  // A trailing "+" is the key itself ("+", "Mod++"), not a separator.
  let parts = text.split("+");
  if (text === "+") parts = ["+"];
  else if (text.endsWith("++")) parts = [...text.slice(0, -2).split("+"), "+"];
  const key = parts.pop();
  if (!key) return null;
  const step: ShortcutStep = { key: key.toLowerCase(), mod: false, ctrl: false, alt: false, shift: false };
  for (const part of parts) {
    const modifier = part.toLowerCase();
    if (!MODIFIERS.has(modifier)) return null;
    step[modifier as "mod" | "ctrl" | "alt" | "shift"] = true;
  }
  return step;
}

export function parseShortcut(text: string): Shortcut | null {
  const words = text.trim().split(/\s+/);
  if (words[0] === "") return null;
  const steps: Shortcut = [];
  for (const word of words) {
    const step = parseStep(word);
    if (!step) return null;
    steps.push(step);
  }
  return steps;
}

function isLetter(key: string): boolean {
  return /^[a-z]$/.test(key);
}

function physicalModifiers(step: ShortcutStep, mac: boolean) {
  return { meta: mac && step.mod, ctrl: step.ctrl || (!mac && step.mod), alt: step.alt };
}

// Alt+letter produces a different character on a Mac (Alt+N is "˜"), so with
// Alt held a letter or digit matches by its physical key code instead.
function keyMatches(key: string, event: KeyboardEvent): boolean {
  if (event.key.toLowerCase() === key) return true;
  if (!event.altKey) return false;
  if (isLetter(key)) return event.code === `Key${key.toUpperCase()}`;
  if (/^[0-9]$/.test(key)) return event.code === `Digit${key}`;
  return false;
}

export function matchesStep(step: ShortcutStep, event: KeyboardEvent, mac: boolean): boolean {
  const modifiers = physicalModifiers(step, mac);
  if (event.metaKey !== modifiers.meta || event.ctrlKey !== modifiers.ctrl || event.altKey !== modifiers.alt) {
    return false;
  }
  // A symbol step ignores Shift, since layouts differ on which symbols need it.
  if ((step.shift || isLetter(step.key)) && event.shiftKey !== step.shift) return false;
  return keyMatches(step.key, event);
}

// The physical keystroke a step stands for on this platform, so "Mod+K" and
// "Ctrl+K" compare equal off a Mac.
function canonicalStep(step: ShortcutStep, mac: boolean): string {
  const { meta, ctrl, alt } = physicalModifiers(step, mac);
  return `${meta ? "meta+" : ""}${ctrl ? "ctrl+" : ""}${alt ? "alt+" : ""}${step.shift ? "shift+" : ""}${step.key}`;
}

// shell-ux.md §7.3: two shortcuts conflict when they're equal or one is the
// start of the other's sequence.
export function shortcutsConflict(a: Shortcut, b: Shortcut, mac: boolean): boolean {
  const length = Math.min(a.length, b.length);
  for (let i = 0; i < length; i++) {
    const stepA = a[i];
    const stepB = b[i];
    if (!stepA || !stepB || canonicalStep(stepA, mac) !== canonicalStep(stepB, mac)) return false;
  }
  return true;
}

const KEY_LABELS: Record<string, string> = {
  escape: "Esc",
  arrowup: "↑",
  arrowdown: "↓",
  arrowleft: "←",
  arrowright: "→",
};

const SPOKEN_KEYS: Record<string, string> = {
  escape: "Escape",
  arrowup: "Up arrow",
  arrowdown: "Down arrow",
  arrowleft: "Left arrow",
  arrowright: "Right arrow",
  "/": "slash",
  "?": "question mark",
  ".": "period",
  ",": "comma",
  "+": "plus",
  "-": "minus",
};

function keyLabel(key: string): string {
  if (KEY_LABELS[key]) return KEY_LABELS[key];
  return key.length === 1 ? key.toUpperCase() : key.charAt(0).toUpperCase() + key.slice(1);
}

// keyboard-shortcuts-dialog.md: Mac glyphs in Apple's ⌃⌥⇧⌘ order with no
// separator; elsewhere spelled-out modifiers separated by spaces.
export function formatStep(step: ShortcutStep, mac: boolean): string {
  const { meta, ctrl, alt } = physicalModifiers(step, mac);
  const key = keyLabel(step.key);
  if (mac) {
    return `${ctrl ? "⌃" : ""}${alt ? "⌥" : ""}${step.shift ? "⇧" : ""}${meta ? "⌘" : ""}${key}`;
  }
  return [ctrl && "Ctrl", alt && "Alt", step.shift && "Shift", key].filter(Boolean).join(" ");
}

// A command's raw `shortcut` string for display; unparseable text shows as is.
export function formatShortcutText(text: string, mac: boolean = IS_MAC): string {
  const shortcut = parseShortcut(text);
  return shortcut ? shortcut.map((step) => formatStep(step, mac)).join(" ") : text;
}

export function describeShortcut(shortcut: Shortcut, mac: boolean): string {
  return shortcut
    .map((step) => {
      const { meta, ctrl, alt } = physicalModifiers(step, mac);
      const key = SPOKEN_KEYS[step.key] ?? keyLabel(step.key);
      return [ctrl && "Control", alt && (mac ? "Option" : "Alt"), step.shift && "Shift", meta && "Command", key]
        .filter(Boolean)
        .join(" ");
    })
    .join(" then ");
}
