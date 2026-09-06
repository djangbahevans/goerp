import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ToastBus } from "./toast.js";

describe("ToastBus", () => {
  it("appends a toast per variant and notifies subscribers", () => {
    const bus = new ToastBus();
    const seen: string[][] = [];
    bus.subscribe((toasts) => seen.push(toasts.map((t) => t.variant)));

    bus.success("saved");
    bus.error("failed");

    expect(bus.getToasts()).toHaveLength(2);
    expect(bus.getToasts()[0]).toMatchObject({ variant: "success", message: "saved" });
    expect(bus.getToasts()[1]).toMatchObject({ variant: "error", message: "failed" });
    expect(seen).toEqual([["success"], ["success", "error"]]);
  });

  it("dismiss(id) removes only that toast", () => {
    const bus = new ToastBus();
    bus.success("a");
    bus.success("b");
    const [first] = bus.getToasts();

    bus.dismiss(first!.id);

    expect(bus.getToasts()).toHaveLength(1);
    expect(bus.getToasts()[0]!.message).toBe("b");
  });

  it("dismiss() with no id clears every toast", () => {
    const bus = new ToastBus();
    bus.success("a");
    bus.warning("b");

    bus.dismiss();

    expect(bus.getToasts()).toHaveLength(0);
  });
});

describe("ToastBus loading → resolved pattern (shell-ux.md §7.1)", () => {
  it("loading() returns an id and does not auto-dismiss", () => {
    const bus = new ToastBus();
    const id = bus.loading("Generating invoice...");

    expect(bus.getToasts()).toEqual([{ id, variant: "loading", message: "Generating invoice...", options: undefined }]);
  });

  it("resolving with { id } replaces the loading toast in place, not appended", () => {
    const bus = new ToastBus();
    bus.info("unrelated");
    const id = bus.loading("Generating invoice...");

    bus.success("Invoice generated", { id });

    expect(bus.getToasts()).toHaveLength(2);
    expect(bus.getToasts()[1]).toMatchObject({ id, variant: "success", message: "Invoice generated" });
  });

  it("options.action is carried onto the toast", () => {
    const bus = new ToastBus();
    const onClick = vi.fn();
    bus.error("Failed to send email", { action: { label: "Retry", onClick } });

    expect(bus.getToasts()[0]?.options?.action).toEqual({ label: "Retry", onClick });
  });
});

describe("ToastBus auto-dismiss (shell-ux.md §7.1)", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("auto-dismisses success/warning/info/error after their documented durations", () => {
    const bus = new ToastBus();
    bus.success("a");
    bus.warning("b");
    bus.info("c");
    bus.error("d");
    expect(bus.getToasts()).toHaveLength(4);

    vi.advanceTimersByTime(4000);
    expect(bus.getToasts().map((t) => t.variant)).toEqual(["warning", "error"]);

    vi.advanceTimersByTime(2000);
    expect(bus.getToasts().map((t) => t.variant)).toEqual(["error"]);

    vi.advanceTimersByTime(2000);
    expect(bus.getToasts()).toHaveLength(0);
  });

  it("never auto-dismisses a loading toast", () => {
    const bus = new ToastBus();
    bus.loading("Generating invoice...");

    vi.advanceTimersByTime(60_000);

    expect(bus.getToasts()).toHaveLength(1);
  });

  it("a custom options.duration overrides the variant default", () => {
    const bus = new ToastBus();
    bus.success("a", { duration: 100 });

    vi.advanceTimersByTime(99);
    expect(bus.getToasts()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(bus.getToasts()).toHaveLength(0);
  });

  it("resolving a loading toast schedules auto-dismiss for its new variant", () => {
    const bus = new ToastBus();
    const id = bus.loading("Generating invoice...");
    bus.success("Invoice generated", { id });

    vi.advanceTimersByTime(4000);

    expect(bus.getToasts()).toHaveLength(0);
  });

  it("dismissing a toast early cancels its pending auto-dismiss timer", () => {
    const bus = new ToastBus();
    bus.success("a");
    const [first] = bus.getToasts();

    bus.dismiss(first?.id);
    // If the timer weren't cancelled, this would throw trying to dismiss
    // an already-removed id — harmless either way, but asserts no crash
    // and no stray toast reappears.
    vi.advanceTimersByTime(4000);

    expect(bus.getToasts()).toHaveLength(0);
  });
});

describe("ToastBus max-visible cap (shell-ux.md §7.1: max 5 toasts)", () => {
  it("drops the oldest toast once a 6th is pushed", () => {
    const bus = new ToastBus();
    for (let i = 1; i <= 6; i++) bus.info(`toast ${i}`);

    expect(bus.getToasts()).toHaveLength(5);
    expect(bus.getToasts().map((t) => t.message)).toEqual(["toast 2", "toast 3", "toast 4", "toast 5", "toast 6"]);
  });

  it("resolving a loading toast in place doesn't count as growing the stack", () => {
    const bus = new ToastBus();
    const id = bus.loading("Generating invoice...");
    for (let i = 1; i <= 5; i++) bus.info(`toast ${i}`);
    expect(bus.getToasts()).toHaveLength(5);
    expect(bus.getToasts().some((t) => t.id === id)).toBe(false);

    // A fresh loading toast now, resolved in place — must not itself
    // trigger the drop-oldest path a second time.
    const id2 = bus.loading("Generating invoice 2...");
    bus.success("done", { id: id2 });

    expect(bus.getToasts()).toHaveLength(5);
  });
});
