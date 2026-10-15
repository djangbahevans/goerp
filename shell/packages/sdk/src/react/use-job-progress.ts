import { useCallback, useSyncExternalStore } from "react";
import { type JobProgress, sseManager } from "../realtime/sse-manager.js";

export type { JobProgress, JobStatus } from "../realtime/sse-manager.js";

const noSubscription = () => {};

// typescript-sdk-reference.md §5 "useJobProgress".
export function useJobProgress(jobId: string | null): JobProgress | null {
  const subscribe = useCallback(
    (onChange: () => void) => (jobId === null ? noSubscription : sseManager.subscribe(jobId, onChange)),
    [jobId],
  );
  const getSnapshot = useCallback(() => (jobId === null ? null : sseManager.progress(jobId)), [jobId]);
  return useSyncExternalStore(subscribe, getSnapshot);
}
