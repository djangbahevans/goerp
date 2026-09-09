import { useAuth } from "@goerp/sdk/auth";
import { useMemo } from "react";
import type { Command } from "./command-types.js";

// shell-architecture.md §18 source 1. Only "Sign Out" is wired up — the
// other example built-ins (navigate to settings, toggle theme, open
// keyboard shortcut help) each need infrastructure that doesn't exist in
// this app yet (a settings route, a theme system, a shortcuts-help
// surface); adding them as commands with nowhere to go would be
// misleading rather than deferred.
export function useBuiltInCommands(): Command[] {
  const { logout } = useAuth();
  return useMemo<Command[]>(
    () => [
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
