import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Countdown } from "./countdown.js";

afterEach(cleanup);

describe("Countdown", () => {
  it("renders the default format on mount", () => {
    render(<Countdown seconds={60} />);
    expect(screen.getByText("60 seconds")).toBeTruthy();
  });

  it("uses the singular form for 1 second in the default format", () => {
    render(<Countdown seconds={1} />);
    expect(screen.getByText("1 second")).toBeTruthy();
  });

  it("uses a caller-supplied format function", () => {
    render(<Countdown seconds={45} format={(n) => `0:${n}`} />);
    expect(screen.getByText("0:45")).toBeTruthy();
  });

  it("ticks down anchored to real elapsed time, not a per-tick decrement", () => {
    vi.useFakeTimers();
    try {
      render(<Countdown seconds={5} />);
      expect(screen.getByText("5 seconds")).toBeTruthy();

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(screen.getByText("3 seconds")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not drift behind wall-clock time when a tick fires late (a throttled background tab)", () => {
    vi.useFakeTimers();
    try {
      render(<Countdown seconds={10} />);

      // One long, delayed jump — as if the tab was throttled and only a
      // single queued interval tick fires after real time has moved on —
      // rather than many on-schedule 200ms ticks.
      act(() => {
        vi.advanceTimersByTime(7000);
      });

      expect(screen.getByText("3 seconds")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("calls onComplete exactly once when it reaches zero, and stops rendering", () => {
    vi.useFakeTimers();
    try {
      const onComplete = vi.fn();
      const { container } = render(<Countdown seconds={2} onComplete={onComplete} />);

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(onComplete).toHaveBeenCalledTimes(1);
      expect(container.textContent).toBe("");

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(onComplete).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("calls onComplete once and renders nothing immediately when given 0 or fewer seconds", () => {
    const onComplete = vi.fn();
    const { container } = render(<Countdown seconds={0} onComplete={onComplete} />);
    expect(container.textContent).toBe("");
    expect(onComplete).toHaveBeenCalledTimes(1);
  });

  it("restarts cleanly from a new duration when seconds changes", () => {
    vi.useFakeTimers();
    try {
      const { rerender } = render(<Countdown seconds={5} />);
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(screen.getByText("2 seconds")).toBeTruthy();

      rerender(<Countdown seconds={30} />);
      expect(screen.getByText("30 seconds")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not display a larger number on the first tick for a fractional seconds prop", () => {
    vi.useFakeTimers();
    try {
      const { container } = render(<Countdown seconds={59.4} />);
      const initialText = container.textContent;
      expect(initialText).toBe("60 seconds");

      act(() => {
        vi.advanceTimersByTime(200);
      });
      // Same reading, not a jump to "61 seconds" — the first tick's own
      // Math.ceil(59.4 - 0.2) must agree with the initial render's rounding.
      expect(container.textContent).toBe(initialText);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops its interval on unmount", () => {
    vi.useFakeTimers();
    try {
      const onComplete = vi.fn();
      const { unmount } = render(<Countdown seconds={2} onComplete={onComplete} />);
      unmount();

      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(onComplete).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
