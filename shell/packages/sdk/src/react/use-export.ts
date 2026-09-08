import { useState } from "react";
import { apiClient } from "../http/api-client.js";
import { downloadBlob } from "../http/download-blob.js";
import { actionRegistry } from "./action-registry.js";

// view-system.md's "Export actions"/"Report actions": a route resolved the
// same way useAction resolves one, but the response is a file body to save
// rather than JSON to parse — apiClient.getBlob + downloadBlob, kept behind
// a hook so app code never needs to reach into @goerp/sdk/http directly,
// matching how useAction hides apiClient/actionRegistry from its callers.
export interface UseExportResult {
  trigger: (params?: Record<string, unknown>) => Promise<void>;
  isPending: boolean;
  isError: boolean;
  error: Error | null;
}

export function useExport(routeName: string, filename: string): UseExportResult {
  const [isPending, setPending] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const trigger = async (params: Record<string, unknown> = {}): Promise<void> => {
    setPending(true);
    setError(null);
    try {
      const route = await actionRegistry.resolve(routeName);
      const blob = await apiClient.getBlob(route.path, { params });
      downloadBlob(blob, filename);
    } catch (err) {
      const asError = err instanceof Error ? err : new Error(String(err));
      setError(asError);
      throw asError;
    } finally {
      setPending(false);
    }
  };

  return { trigger, isPending, isError: error !== null, error };
}
