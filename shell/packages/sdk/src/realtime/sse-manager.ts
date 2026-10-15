import { AppError } from "../error/app-error.js";

export type JobStatus = "pending" | "running" | "complete" | "failed";

export interface JobProgress {
  jobId: string;
  status: JobStatus;
  // 0–100.
  percent: number;
  message: string;
  // Set only when status is "complete" and the job handler reported a result.
  result?: unknown;
  // Set only when status is "failed": code "jobs.failed", "jobs.cancelled" or
  // "jobs.progress_unavailable".
  error?: AppError;
  startedAt: Date | null;
  completedAt: Date | null;
}

interface JobSnapshot {
  job_id: string;
  status: JobStatus;
  percent: number;
  message: string;
  result?: unknown;
  error?: { code: string; message: string };
  started_at: string | null;
  completed_at: string | null;
}

type Listener = (progress: JobProgress) => void;

interface Stream {
  source: EventSource;
  listeners: Set<Listener>;
  last: JobProgress | null;
}

function toJobProgress(snapshot: JobSnapshot): JobProgress {
  const progress: JobProgress = {
    jobId: snapshot.job_id,
    status: snapshot.status,
    percent: snapshot.percent,
    message: snapshot.message,
    startedAt: snapshot.started_at === null ? null : new Date(snapshot.started_at),
    completedAt: snapshot.completed_at === null ? null : new Date(snapshot.completed_at),
  };
  if (snapshot.status === "complete" && snapshot.result !== undefined) progress.result = snapshot.result;
  if (snapshot.status === "failed" && snapshot.error) {
    progress.error = new AppError({ ...snapshot.error, httpStatus: 0 });
  }
  return progress;
}

function unavailable(jobId: string): JobProgress {
  return {
    jobId,
    status: "failed",
    percent: 0,
    message: "",
    error: new AppError({
      code: "jobs.progress_unavailable",
      message: "This job's progress is not available",
      httpStatus: 0,
    }),
    startedAt: null,
    completedAt: null,
  };
}

// Owns the EventSource connections to the engine's job progress stream, one
// per job however many subscribers follow it.
export class SSEManager {
  private readonly streams = new Map<string, Stream>();

  subscribe(jobId: string, listener: Listener): () => void {
    const stream = this.streams.get(jobId) ?? this.open(jobId);
    stream.listeners.add(listener);
    return () => {
      stream.listeners.delete(listener);
      if (stream.listeners.size === 0 && this.streams.get(jobId) === stream) {
        stream.source.close();
        this.streams.delete(jobId);
      }
    };
  }

  // A finished stream keeps its terminal value while it has subscribers.
  progress(jobId: string): JobProgress | null {
    return this.streams.get(jobId)?.last ?? null;
  }

  private open(jobId: string): Stream {
    // EventSource cannot send an Authorization header; the session cookie
    // authenticates the stream.
    const source = new EventSource(`/_jobs/progress/${encodeURIComponent(jobId)}`, { withCredentials: true });
    const stream: Stream = { source, listeners: new Set(), last: null };
    const emit = (progress: JobProgress) => {
      stream.last = progress;
      for (const listener of stream.listeners) listener(progress);
    };

    source.onmessage = (event: MessageEvent<string>) => emit(toJobProgress(JSON.parse(event.data) as JobSnapshot));
    // Closing here stops the browser from reconnecting to a finished stream.
    source.addEventListener("done", () => source.close());
    source.onerror = () => {
      // CONNECTING: the browser is retrying a dropped connection.
      // CLOSED: the engine refused the stream, and the browser has given up.
      if (source.readyState === EventSource.CLOSED && stream.last === null) emit(unavailable(jobId));
    };
    this.streams.set(jobId, stream);
    return stream;
  }
}

export const sseManager = new SSEManager();
