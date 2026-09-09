import { describe, expect, it, vi } from "vitest";
import { CommandRegistry } from "./command-registry.js";
import type { Command } from "./command-types.js";

function command(id: string): Command {
  return { id, label: id, action: vi.fn() };
}

describe("CommandRegistry", () => {
  it("returns commands from every registered batch, flattened", () => {
    const registry = new CommandRegistry();
    registry.register([command("a"), command("b")]);
    registry.register([command("c")]);
    expect(
      registry
        .getAll()
        .map((c) => c.id)
        .sort(),
    ).toEqual(["a", "b", "c"]);
  });

  it("drops a batch once its unregister function is called", () => {
    const registry = new CommandRegistry();
    const unregister = registry.register([command("a")]);
    registry.register([command("b")]);
    unregister();
    expect(registry.getAll().map((c) => c.id)).toEqual(["b"]);
  });

  it("notifies subscribers on register and unregister", () => {
    const registry = new CommandRegistry();
    const listener = vi.fn();
    registry.subscribe(listener);

    const unregister = registry.register([command("a")]);
    expect(listener).toHaveBeenCalledTimes(1);

    unregister();
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("stops notifying after the subscription itself is cancelled", () => {
    const registry = new CommandRegistry();
    const listener = vi.fn();
    const unsubscribe = registry.subscribe(listener);
    unsubscribe();

    registry.register([command("a")]);
    expect(listener).not.toHaveBeenCalled();
  });

  it("returns the same array reference across calls when nothing changed", () => {
    // useSyncExternalStore's snapshot getter must be stable between
    // mutations, or every render looks like a store change.
    const registry = new CommandRegistry();
    registry.register([command("a")]);
    expect(registry.getAll()).toBe(registry.getAll());
  });
});
