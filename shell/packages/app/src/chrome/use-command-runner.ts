import { useAuth } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import type { Command, CommandContext } from "./command-types.js";

// Runs a command the same way from the palette and from its keyboard
// shortcut. Returns false, after a toast, when there's no signed-in user
// and tenant for the command's context yet.
export function useCommandRunner(): (command: Command) => boolean {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { user, tenant } = useAuth();

  return useCallback(
    (command: Command) => {
      // Mounted at the app root regardless of auth state, so user/tenant can
      // genuinely be null here (e.g. on the login screen).
      if (!user || !tenant) {
        toast.error("Not ready yet — try again in a moment.");
        return false;
      }
      const context: CommandContext = {
        navigate: (path) => void navigate({ to: path }),
        user,
        tenant,
        toast,
        queryClient,
      };
      // A module-registered command's action may be async (CommandDefinition
      // permits void | Promise<void>). The executor runs it synchronously and
      // turns a throw or a rejection alike into a toast.
      void new Promise<void>((resolve) => resolve(command.action(context))).catch((err: unknown) => {
        toast.error(err instanceof Error ? err.message : String(err));
      });
      return true;
    },
    [navigate, queryClient, user, tenant],
  );
}
