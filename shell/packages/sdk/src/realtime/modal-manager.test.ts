import { describe, expect, it, vi } from "vitest";
import { type DialogConfig, ModalManager } from "./modal-manager.js";

const dialog: DialogConfig = {
  title: "Request payment",
  submitLabel: "Send",
  action: { method: "POST", path: "/sales/invoices/01j/request-payment" },
  fields: [],
  record: null,
  values: {},
};

describe("ModalManager", () => {
  it("stacks form and dialog entries in the order they open and notifies subscribers", () => {
    const manager = new ModalManager();
    const listener = vi.fn();
    manager.subscribe(listener);

    manager.open({ resource: "sales.invoice", recordId: "01j" });
    void manager.openDialog(dialog);

    expect(manager.getStack().map((entry) => entry.kind)).toEqual(["form", "dialog"]);
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("closeTop cancels and removes the topmost dialog, leaving the form beneath", async () => {
    const manager = new ModalManager();
    manager.open({ resource: "sales.invoice" });
    const result = manager.openDialog(dialog);

    manager.closeTop();

    await expect(result).resolves.toEqual({ cancelled: true });
    expect(manager.getStack().map((entry) => entry.kind)).toEqual(["form"]);
  });

  it("closeTop with an empty stack is a no-op", () => {
    const manager = new ModalManager();
    const listener = vi.fn();
    manager.subscribe(listener);

    manager.closeTop();

    expect(manager.getStack()).toEqual([]);
    expect(listener).not.toHaveBeenCalled();
  });

  it("stops notifying after unsubscribe", () => {
    const manager = new ModalManager();
    const listener = vi.fn();
    manager.subscribe(listener)();

    manager.open({ resource: "sales.invoice" });

    expect(listener).not.toHaveBeenCalled();
  });
});
