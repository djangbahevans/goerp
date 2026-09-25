import { parseShortcut, type Shortcut } from "./shortcut.js";

export type ShortcutGroup = "General" | "Navigation" | "Commands";

export type ShellShortcutId =
  | "command-palette"
  | "keyboard-shortcuts"
  | "close-overlay"
  | "go-home"
  | "go-settings"
  | "go-admin";

export interface ShellShortcut {
  id: ShellShortcutId;
  label: string;
  group: ShortcutGroup;
  shortcuts: Shortcut[];
}

function shortcut(text: string): Shortcut {
  const parsed = parseShortcut(text);
  if (!parsed) throw new Error(`invalid shell shortcut "${text}"`);
  return parsed;
}

// shell-ux.md §7.3, in its table's order.
export const SHELL_SHORTCUTS: readonly ShellShortcut[] = [
  { id: "command-palette", label: "Open command palette", group: "General", shortcuts: [shortcut("Mod+K")] },
  {
    id: "keyboard-shortcuts",
    label: "Open keyboard shortcuts",
    group: "General",
    shortcuts: [shortcut("Mod+/"), shortcut("?")],
  },
  { id: "close-overlay", label: "Close the topmost overlay", group: "General", shortcuts: [shortcut("Escape")] },
  { id: "go-home", label: "Go home", group: "Navigation", shortcuts: [shortcut("G H")] },
  { id: "go-settings", label: "Go to settings", group: "Navigation", shortcuts: [shortcut("G S")] },
  { id: "go-admin", label: "Go to admin", group: "Navigation", shortcuts: [shortcut("G A")] },
];
