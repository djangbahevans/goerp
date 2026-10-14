import { useAuth } from "@goerp/sdk/auth";
import { useNavigate } from "@goerp/sdk/nav";
import { toast } from "@goerp/sdk/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { Command, CommandContext } from "./command-types.js";

export function useCommandRunner(): (command: Command) => boolean {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { user, tenant } = useAuth();

  return useCallback(
    (command: Command) => {
      if (!user || !tenant) {
        toast.error("Not ready yet — try again in a moment.");
        return false;
      }
      const context: CommandContext = {
        navigate,
        user,
        tenant,
        toast,
        queryClient,
      };
      void new Promise<void>((resolve) => resolve(command.action(context))).catch((err: unknown) => {
        toast.error(err instanceof Error ? err.message : String(err));
      });
      return true;
    },
    [navigate, queryClient, user, tenant],
  );
}
