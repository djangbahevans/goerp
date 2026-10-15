import { act, cleanup, renderHook } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FakeEventSource, source } from "./fake-event-source.js";
import { type UseSSEOptions, useSSE } from "./use-sse.js";

interface LogLine {
  line: string;
}

function renderSSE(initial: { url?: string } & UseSSEOptions<LogLine> = {}) {
  return renderHook(({ url = "/contacts/imports/i1/log", ...options }) => useSSE<LogLine>(url, options), {
    initialProps: initial,
  });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("useSSE", () => {
  it("opens the url with credentials and moves from connecting to connected", () => {
    const { result } = renderSSE();

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(source().url).toBe("/contacts/imports/i1/log");
    expect(source().init).toEqual({ withCredentials: true });
    expect(result.current).toMatchObject({ status: "connecting", data: null });

    source().open();
    expect(result.current.status).toBe("connected");
  });

  it("holds the latest parsed message and calls onMessage once per message, in order", () => {
    const onMessage = vi.fn();
    const { result } = renderSSE({ onMessage });
    source().open();

    source().message({ line: "row 1 imported" });
    expect(result.current.data).toEqual({ line: "row 1 imported" });
    source().message({ line: "row 2 imported" });
    expect(result.current.data).toEqual({ line: "row 2 imported" });

    expect(onMessage.mock.calls).toEqual([[{ line: "row 1 imported" }], [{ line: "row 2 imported" }]]);
  });

  it("closes the connection on the done event, keeping the last message", () => {
    const onDone = vi.fn();
    const onError = vi.fn();
    const { result } = renderSSE({ onDone, onError });
    source().open();
    source().message({ line: "finished" });

    source().done();

    expect(source().readyState).toBe(FakeEventSource.CLOSED);
    expect(result.current).toMatchObject({ status: "done", data: { line: "finished" } });
    expect(onDone).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
  });

  it("returns to connecting while the browser retries, and reconnects without an error", () => {
    const onError = vi.fn();
    const { result } = renderSSE({ onError });
    source().open();

    source().drop();
    expect(result.current.status).toBe("connecting");

    source().open();
    expect(result.current.status).toBe("connected");
    expect(onError).not.toHaveBeenCalled();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("reports an error once the browser stops retrying", () => {
    const onError = vi.fn();
    const onDone = vi.fn();
    const { result } = renderSSE({ onError, onDone });

    const event = source().refuse();

    expect(result.current.status).toBe("error");
    expect(onError.mock.calls).toEqual([[event]]);
    expect(onDone).not.toHaveBeenCalled();
  });

  it("close() ends the stream as done without calling onDone or onError", () => {
    const onMessage = vi.fn();
    const onDone = vi.fn();
    const onError = vi.fn();
    const { result } = renderSSE({ onMessage, onDone, onError });
    source().open();

    act(() => result.current.close());
    expect(source().readyState).toBe(FakeEventSource.CLOSED);
    expect(result.current.status).toBe("done");

    source().message({ line: "late" });
    source().done();
    source().refuse();
    expect(result.current).toMatchObject({ status: "done", data: null });
    expect(onMessage).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  it("opens nothing while enabled is false, and connects once it turns true", () => {
    const { result, rerender } = renderSSE({ enabled: false });
    expect(FakeEventSource.instances).toHaveLength(0);
    expect(result.current.status).toBe("connecting");

    rerender({ enabled: true });
    expect(FakeEventSource.instances).toHaveLength(1);
    source().open();
    expect(result.current.status).toBe("connected");
  });

  it("closes the connection when enabled turns false", () => {
    const { result, rerender } = renderSSE({ enabled: true });
    source().open();
    source().message({ line: "row 1 imported" });

    rerender({ enabled: false });

    expect(source().readyState).toBe(FakeEventSource.CLOSED);
    expect(result.current).toMatchObject({ status: "connecting", data: null });
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("closes the previous connection and opens a new one when url changes", () => {
    const { result, rerender } = renderSSE({ url: "/contacts/imports/i1/log" });
    source().open();
    source().message({ line: "i1 finished" });
    source().done();
    expect(result.current.status).toBe("done");

    rerender({ url: "/contacts/imports/i2/log" });

    expect(FakeEventSource.instances).toHaveLength(2);
    expect(source(0).readyState).toBe(FakeEventSource.CLOSED);
    expect(source(1).url).toBe("/contacts/imports/i2/log");
    expect(result.current).toMatchObject({ status: "connecting", data: null });

    source(1).open();
    expect(result.current.status).toBe("connected");
  });

  it("never commits the previous stream's status for a new url", () => {
    const committed: string[] = [];
    const { rerender } = renderHook(
      ({ url }) => {
        const { status } = useSSE<LogLine>(url);
        useEffect(() => {
          committed.push(`${url} ${status}`);
        });
      },
      { initialProps: { url: "/a" } },
    );
    source().done();

    rerender({ url: "/b" });

    expect(committed).toEqual(["/a connecting", "/a done", "/b connecting"]);
  });

  it("closes the connection on unmount", () => {
    const { unmount } = renderSSE();
    source().open();

    unmount();

    expect(source().readyState).toBe(FakeEventSource.CLOSED);
  });

  it("delivers each event to the callbacks of the latest render without reconnecting", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderSSE({ onMessage: first });
    source().open();

    rerender({ onMessage: second });
    source().message({ line: "row 1 imported" });

    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
    expect(FakeEventSource.instances).toHaveLength(1);
  });
});
