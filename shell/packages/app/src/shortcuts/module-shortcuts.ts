import type { Command } from "../chrome/command-types.js";
import { parseShortcut, type Shortcut, shortcutsConflict } from "./shortcut.js";

export interface ModuleShortcut {
  command: Command;
  shortcut: Shortcut;
}

// shell-ux.md §7.3 "Shortcut conflicts": a module shortcut conflicting with a
// shell one, or with an earlier-registered module one, is dropped.
export function resolveModuleShortcuts(
  commands: readonly Command[],
  shellShortcuts: readonly Shortcut[],
  mac: boolean,
  warn: (message: string) => void,
): ModuleShortcut[] {
  const accepted: ModuleShortcut[] = [];
  for (const command of commands) {
    if (!command.shortcut) continue;
    const shortcut = parseShortcut(command.shortcut);
    if (!shortcut) {
      warn(`Command "${command.id}" has an invalid shortcut "${command.shortcut}" and gets no active shortcut.`);
      continue;
    }
    if (shellShortcuts.some((reserved) => shortcutsConflict(reserved, shortcut, mac))) {
      warn(`Command "${command.id}" shortcut "${command.shortcut}" conflicts with a shell shortcut and is ignored.`);
      continue;
    }
    const rival = accepted.find((other) => shortcutsConflict(other.shortcut, shortcut, mac));
    if (rival) {
      warn(
        `Command "${command.id}" shortcut "${command.shortcut}" conflicts with command "${rival.command.id}" and is ignored.`,
      );
      continue;
    }
    accepted.push({ command, shortcut });
  }
  return accepted;
}

const warned = new Set<string>();

// Once per message, since the registry re-resolves on every registration.
export function warnInDevelopment(message: string): void {
  if (!import.meta.env.DEV || warned.has(message)) return;
  warned.add(message);
  console.warn(message);
}
