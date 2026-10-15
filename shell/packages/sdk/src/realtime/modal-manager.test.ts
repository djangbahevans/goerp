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

  it("close removes the entry with that id, wherever it is in the stack", async () => {
    const manager = new ModalManager();
    const first = manager.openDialog(dialog);
    manager.open({ resource: "sales.invoice" });
    const [entry] = manager.getStack();

    manager.close(entry?.id ?? -1);

    await expect(first).resolves.toEqual({ cancelled: true });
    expect(manager.getStack().map((e) => e.kind)).toEqual(["form"]);
  });

  it("submitDialog resolves the dialog as submitted after the submit finishes, then closes it", async () => {
    const manager = new ModalManager();
    const result = manager.openDialog(dialog);
    const id = manager.getStack()[0]?.id ?? -1;
    const settled = vi.fn();
    void result.then(settled);
    let finish: () => void = () => {};

    const submitting = manager.submitDialog(id, () => new Promise<void>((resolve) => (finish = resolve)));
    await Promise.resolve();
    expect(settled).not.toHaveBeenCalled();
    expect(manager.getStack()).toHaveLength(1);

    finish();
    await submitting;
    await expect(result).resolves.toEqual({ cancelled: false });
    expect(manager.getStack()).toEqual([]);
  });

  it("a close_modal during a submit removes the dialog but leaves it resolving as submitted", async () => {
    const manager = new ModalManager();
    const result = manager.openDialog(dialog);
    const id = manager.getStack()[0]?.id ?? -1;
    const settled = vi.fn();
    void result.then(settled);

    await manager.submitDialog(id, async () => {
      manager.closeTop();
      expect(manager.getStack()).toEqual([]);
      await Promise.resolve();
      expect(settled).not.toHaveBeenCalled();
    });

    await expect(result).resolves.toEqual({ cancelled: false });
  });

  it("a rejected submit keeps the dialog open and unresolved, and rethrows", async () => {
    const manager = new ModalManager();
    const result = manager.openDialog(dialog);
    const id = manager.getStack()[0]?.id ?? -1;
    const settled = vi.fn();
    void result.then(settled);

    await expect(manager.submitDialog(id, () => Promise.reject(new Error("bad phone")))).rejects.toThrow("bad phone");
    await Promise.resolve();

    expect(settled).not.toHaveBeenCalled();
    expect(manager.getStack()).toHaveLength(1);
    manager.closeTop();
    await expect(result).resolves.toEqual({ cancelled: true });
  });

  it("submitDialog rejects for an id that is not an open dialog", async () => {
    const manager = new ModalManager();
    manager.open({ resource: "sales.invoice" });
    const submit = vi.fn(() => Promise.resolve());

    await expect(manager.submitDialog(manager.getStack()[0]?.id ?? -1, submit)).rejects.toThrow();
    expect(submit).not.toHaveBeenCalled();
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
