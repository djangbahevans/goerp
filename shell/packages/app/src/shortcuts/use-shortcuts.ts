import { PermissionContext, useAuth } from "@goerp/sdk/auth";
import { useNavigate } from "@tanstack/react-router";
import { useContext, useMemo, useSyncExternalStore } from "react";
import { openCommandPalette } from "../chrome/command-palette-control.js";
import { commandRegistry } from "../chrome/command-registry.js";
import { useCommandRunner } from "../chrome/use-command-runner.js";
import { openKeyboardShortcuts } from "./keyboard-shortcuts-control.js";
import { resolveModuleShortcuts, warnInDevelopment } from "./module-shortcuts.js";
import { SHELL_SHORTCUTS, type ShellShortcutId, type ShortcutGroup } from "./shell-shortcuts.js";
import { IS_MAC, type Shortcut } from "./shortcut.js";

export interface ShortcutEntry {
  id: string;
  label: string;
  group: ShortcutGroup;
  shortcuts: Shortcut[];
  // Absent for Esc, which every overlay handles itself (shell-ux.md §7.3).
  run?: () => void;
}

const RESERVED = SHELL_SHORTCUTS.flatMap((s) => s.shortcuts);

// The shortcuts active for the current user, in the reference's display
// order: the shell's own, then module commands'.
export function useShortcuts(): ShortcutEntry[] {
  const navigate = useNavigate();
  const { user } = useAuth();
  const runCommand = useCommandRunner();
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("useShortcuts must be used within a PermissionProvider");
  }
  const commands = useSyncExternalStore(
    (listener) => commandRegistry.subscribe(listener),
    () => commandRegistry.getAll(),
  );
  // Permission first, so a command the user can't run never claims a
  // shortcut from one they can.
  const moduleShortcuts = useMemo(
    () =>
      resolveModuleShortcuts(
        commands.filter((command) => !command.permission || permissions.check(command.permission)),
        RESERVED,
        IS_MAC,
        warnInDevelopment,
      ),
    [commands, permissions],
  );
  const isAdmin = user?.roles.includes("admin") ?? false;

  return useMemo(() => {
    const go = (path: string) => () => void navigate({ to: path });
    const runs: Record<ShellShortcutId, (() => void) | undefined> = {
      "command-palette": openCommandPalette,
      "keyboard-shortcuts": openKeyboardShortcuts,
      "close-overlay": undefined,
      "go-home": user ? go("/") : undefined,
      "go-settings": user ? go("/settings/profile") : undefined,
      "go-admin": isAdmin ? go("/admin") : undefined,
    };
    const shell = SHELL_SHORTCUTS.filter((s) => s.group === "General" || runs[s.id]).map(
      ({ id, label, group, shortcuts }): ShortcutEntry => {
        const run = runs[id];
        return run ? { id, label, group, shortcuts, run } : { id, label, group, shortcuts };
      },
    );
    const modules = moduleShortcuts
      .toSorted((a, b) => a.command.label.localeCompare(b.command.label))
      .map(
        ({ command, shortcut }): ShortcutEntry => ({
          id: command.id,
          label: command.label,
          group: "Commands",
          shortcuts: [shortcut],
          run: () => runCommand(command),
        }),
      );
    return [...shell, ...modules];
  }, [navigate, user, isAdmin, moduleShortcuts, runCommand]);
}
