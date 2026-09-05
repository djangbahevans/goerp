import { describe, expect, it } from "vitest";
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
