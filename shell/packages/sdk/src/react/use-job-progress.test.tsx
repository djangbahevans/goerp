import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { FakeEventSource, source } from "../realtime/fake-event-source.js";
import { useJobProgress } from "./use-job-progress.js";

function snapshot(fields: Record<string, unknown> = {}) {
  return {
    job_id: "job_4821",
    status: "running",
    percent: 40,
    message: "Imported 400 of 1000",
    started_at: "2026-05-16T10:02:11Z",
    completed_at: null,
    ...fields,
  };
}

function renderJobProgress(jobId: string | null = "job_4821") {
  return renderHook(({ id }) => useJobProgress(id), { initialProps: { id: jobId } });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("useJobProgress", () => {
  it("returns null and opens nothing while jobId is null", () => {
    const { result, rerender } = renderJobProgress(null);

    expect(result.current).toBeNull();
    expect(FakeEventSource.instances).toHaveLength(0);

    rerender({ id: "job_4821" });
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(source().url).toBe("/_jobs/progress/job_4821");
    expect(source().init).toEqual({ withCredentials: true });
  });

  it("returns null until the first snapshot, then moves from pending through running to complete", () => {
    const { result } = renderJobProgress();
    source().open();
    expect(result.current).toBeNull();

    source().message(snapshot({ status: "pending", percent: 0, message: "", started_at: null }));
    expect(result.current).toEqual({
      jobId: "job_4821",
      status: "pending",
      percent: 0,
      message: "",
      startedAt: null,
      completedAt: null,
    });

    source().message(snapshot());
    expect(result.current).toEqual({
      jobId: "job_4821",
      status: "running",
      percent: 40,
      message: "Imported 400 of 1000",
      startedAt: new Date("2026-05-16T10:02:11Z"),
      completedAt: null,
    });

    source().message(
      snapshot({
        status: "complete",
        percent: 100,
        message: "Imported 1000 of 1000",
        result: { created: 988, failed: 12 },
        completed_at: "2026-05-16T10:03:40Z",
      }),
    );
    expect(result.current).toEqual({
      jobId: "job_4821",
      status: "complete",
      percent: 100,
      message: "Imported 1000 of 1000",
      result: { created: 988, failed: 12 },
      startedAt: new Date("2026-05-16T10:02:11Z"),
      completedAt: new Date("2026-05-16T10:03:40Z"),
    });
  });

  it("keeps the final value after the stream ends, without reconnecting", () => {
    const { result } = renderJobProgress();
    source().open();
    source().message(snapshot({ status: "complete", percent: 100, completed_at: "2026-05-16T10:03:40Z" }));

    source().done();

    expect(source().readyState).toBe(FakeEventSource.CLOSED);
    expect(result.current?.status).toBe("complete");
    expect(result.current).not.toHaveProperty("result");
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it.each(["jobs.failed", "jobs.cancelled"])("reports a %s failure as an AppError", (code) => {
    const { result } = renderJobProgress();
    source().open();

    source().message(
      snapshot({
        status: "failed",
        percent: 46,
        error: { code, message: "The job failed" },
        completed_at: "2026-05-16T10:03:40Z",
      }),
    );
    source().done();

    expect(result.current).toMatchObject({ status: "failed", percent: 46, message: "Imported 400 of 1000" });
    expect(result.current).not.toHaveProperty("result");
    expect(result.current?.error).toBeInstanceOf(AppError);
    expect(result.current?.error).toMatchObject({ code, message: "The job failed" });
  });

  it("sets result only on complete and error only on failed", () => {
    const { result } = renderJobProgress();
    source().open();

    source().message(snapshot({ result: { created: 1 }, error: { code: "jobs.failed", message: "stray" } }));

    expect(result.current).not.toHaveProperty("result");
    expect(result.current).not.toHaveProperty("error");
  });

  it("reports jobs.progress_unavailable when the stream cannot be opened", () => {
    const { result } = renderJobProgress();

    source().refuse();

    expect(result.current).toEqual({
      jobId: "job_4821",
      status: "failed",
      percent: 0,
      message: "",
      error: expect.any(AppError),
      startedAt: null,
      completedAt: null,
    });
    expect(result.current?.error?.code).toBe("jobs.progress_unavailable");
  });

  it("keeps the value across a dropped connection and continues after the browser reconnects", () => {
    const { result } = renderJobProgress();
    source().open();
    source().message(snapshot());
    const before = result.current;

    source().drop();
    expect(result.current).toBe(before);

    source().open();
    source().message(snapshot({ percent: 60 }));
    expect(result.current?.percent).toBe(60);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("keeps the last snapshot when a reconnect is refused", () => {
    const { result } = renderJobProgress();
    source().open();
    source().message(snapshot());
    const before = result.current;

    source().refuse();

    expect(result.current).toBe(before);
  });

  it("shares one connection between components following the same job", () => {
    const first = renderJobProgress();
    source().open();
    source().message(snapshot());

    const second = renderJobProgress();

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(second.result.current).toBe(first.result.current);

    source().message(snapshot({ percent: 60 }));
    expect(first.result.current?.percent).toBe(60);
    expect(second.result.current?.percent).toBe(60);

    first.unmount();
    expect(source().readyState).toBe(FakeEventSource.OPEN);
    second.unmount();
    expect(source().readyState).toBe(FakeEventSource.CLOSED);
  });

  it("closes the stream and returns null when jobId changes", () => {
    const { result, rerender } = renderJobProgress("job_1");
    source().open();
    source().message(snapshot({ job_id: "job_1", status: "complete", percent: 100 }));
    source().done();

    rerender({ id: "job_2" });

    expect(result.current).toBeNull();
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(source(1).url).toBe("/_jobs/progress/job_2");

    rerender({ id: null });
    expect(result.current).toBeNull();
    expect(source(1).readyState).toBe(FakeEventSource.CLOSED);
  });

  it("closes the stream on unmount", () => {
    const { unmount } = renderJobProgress();
    source().open();

    unmount();

    expect(source().readyState).toBe(FakeEventSource.CLOSED);
  });
});
