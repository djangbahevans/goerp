import { toast } from "@goerp/sdk/notifications";
import { act, renderHook } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useOptimisticMutation } from "./use-optimistic-mutation.js";

afterEach(() => {
  vi.restoreAllMocks();
});

function setup() {
  return renderHook(() => {
    const [value, setValue] = useState(0);
    const runOptimisticMutation = useOptimisticMutation(setValue);
    return { value, setValue, runOptimisticMutation };
  });
}

describe("useOptimisticMutation", () => {
  it("applies the update immediately, before the commit resolves", async () => {
    const { result } = setup();
    let resolveCommit: () => void = () => {};
    const commit = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveCommit = resolve;
        }),
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.runOptimisticMutation({
        apply: (current) => current + 1,
        revert: (current) => current - 1,
        commit,
        errorMessage: () => "failed",
        logContext: "test",
      });
    });

    expect(result.current.value).toBe(1);
    resolveCommit();
    await act(() => pending);
    expect(result.current.value).toBe(1);
  });

  it("leaves the applied state in place and reports nothing on a successful commit", async () => {
    const toastSpy = vi.spyOn(toast, "error").mockImplementation(() => {});
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = setup();

    await act(() =>
      result.current.runOptimisticMutation({
        apply: (current) => current + 1,
        revert: (current) => current - 1,
        commit: vi.fn().mockResolvedValue(undefined),
        errorMessage: () => "failed",
        logContext: "test",
      }),
    );

    expect(result.current.value).toBe(1);
    expect(toastSpy).not.toHaveBeenCalled();
    expect(errorSpy).not.toHaveBeenCalled();
  });

  it("never evaluates errorMessage on a successful commit", async () => {
    vi.spyOn(toast, "error").mockImplementation(() => {});
    const { result } = setup();
    const errorMessage = vi.fn(() => "failed");

    await act(() =>
      result.current.runOptimisticMutation({
        apply: (current) => current + 1,
        revert: (current) => current - 1,
        commit: vi.fn().mockResolvedValue(undefined),
        errorMessage,
        logContext: "test",
      }),
    );

    expect(errorMessage).not.toHaveBeenCalled();
  });

  it("reverts against the current state on failure, not a stale pre-apply snapshot", async () => {
    const toastSpy = vi.spyOn(toast, "error").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = setup();
    let rejectCommit: (error: Error) => void = () => {};
    const commit = vi.fn(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectCommit = reject;
        }),
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.runOptimisticMutation({
        apply: (current) => current + 1,
        revert: (current) => current - 100,
        commit,
        errorMessage: () => "Couldn't save. Please try again.",
        logContext: "test: commit failed",
      });
    });
    expect(result.current.value).toBe(1);

    // An unrelated concurrent update lands while the commit is still in flight.
    act(() => result.current.setValue((current) => current + 10));
    expect(result.current.value).toBe(11);

    act(() => rejectCommit(new Error("network error")));
    await act(() => pending.catch(() => {}));

    expect(result.current.value).toBe(-89);
    expect(toastSpy).toHaveBeenCalledWith("Couldn't save. Please try again.");
  });

  it("logs the error with the given context on failure", async () => {
    vi.spyOn(toast, "error").mockImplementation(() => {});
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = setup();
    const error = new Error("boom");

    await act(() =>
      result.current.runOptimisticMutation({
        apply: (current) => current + 1,
        revert: (current) => current - 1,
        commit: vi.fn().mockRejectedValue(error),
        errorMessage: () => "failed",
        logContext: "test: commit failed",
      }),
    );

    expect(errorSpy).toHaveBeenCalledWith("test: commit failed", error);
  });
});
