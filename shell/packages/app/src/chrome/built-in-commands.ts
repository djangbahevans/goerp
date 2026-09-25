import { useAuth } from "@goerp/sdk/auth";
import { useMemo } from "react";
import { openKeyboardShortcuts } from "../shortcuts/keyboard-shortcuts-control.js";
import type { Command } from "./command-types.js";

// shell-architecture.md §18 source 1.
export function useBuiltInCommands(): Command[] {
  const { logout } = useAuth();
  return useMemo<Command[]>(
    () => [
      {
        id: "builtin.keyboard-shortcuts",
        label: "Keyboard shortcuts",
        group: "Commands",
        keywords: ["hotkeys", "help"],
        shortcut: "Mod+/",
        action: openKeyboardShortcuts,
      },
      {
        id: "builtin.sign-out",
        label: "Sign Out",
        group: "Commands",
        action: () => {
          void logout();
        },
      },
    ],
    [logout],
  );
}
