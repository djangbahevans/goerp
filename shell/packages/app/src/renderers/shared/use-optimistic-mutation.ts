import { toast } from "@goerp/sdk/notifications";
import type { Dispatch, SetStateAction } from "react";
import { useCallback } from "react";

export interface OptimisticUpdate<TState> {
  // Applied immediately, against whatever state is current when this runs.
  apply: (current: TState) => TState;
  // Applied on failure, against whatever state is current at that point —
  // never a snapshot from before `apply`, so an unrelated concurrent
  // update survives the revert.
  revert: (current: TState) => TState;
  commit: () => Promise<void>;
  // Deferred: only evaluated on failure, so a caller whose message is
  // itself a lookup (e.g. a title scan) doesn't pay for it on every
  // successful commit.
  errorMessage: () => string;
  logContext: string;
}

// Shared by KanbanBoard.moveCard and TimelineChart.commitChange: apply a
// change optimistically, await the server call, and on failure revert
// against whatever is currently displayed while surfacing a Toast.error.
export function useOptimisticMutation<TState>(setState: Dispatch<SetStateAction<TState>>) {
  return useCallback(
    async function runOptimisticMutation({
      apply,
      revert,
      commit,
      errorMessage,
      logContext,
    }: OptimisticUpdate<TState>): Promise<void> {
      setState(apply);
      try {
        await commit();
      } catch (error) {
        setState(revert);
        toast.error(errorMessage());
        console.error(logContext, error);
      }
    },
    [setState],
  );
}
