import { createContext, useContext } from "react";

// typescript-sdk-reference.md §5 "useBulkAction" — selection context handed
// to a manifest bulk_actions type:"custom" component after the shell mounts
// it, and (via the same context) to BulkActionPanel itself so it can wire
// Escape to the identical cancellation path a Cancel button uses.
export interface BulkActionContextValue {
  selectedIds: string[];
  selectedCount: number;
  onComplete: () => void;
  onCancel: () => void;
  isLoading: boolean;
  setLoading: (loading: boolean) => void;
}

export const BulkActionContext = createContext<BulkActionContextValue | null>(null);

export function useBulkAction(): BulkActionContextValue {
  const value = useContext(BulkActionContext);
  if (!value) {
    throw new Error("useBulkAction must be used within a bulk action's BulkActionContext.Provider");
  }
  return value;
}
