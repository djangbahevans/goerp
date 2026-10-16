import { useAuth } from "@goerp/sdk/auth";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

// Signing in again happens in the same document, so without this the next user is served the
// previous user's still-fresh queries. An expired session keeps its cache for the page under the
// session-expired modal.
export function useClearQueriesOnSignOut(): void {
  const { state } = useAuth();
  const queryClient = useQueryClient();
  const loggingOut = state.status === "logging_out";

  useEffect(() => {
    if (!loggingOut) return;
    return () => queryClient.clear();
  }, [loggingOut, queryClient]);
}
