import { describe, expect, it, vi } from "vitest";
import { ModuleApiRegistry } from "./module-api-registry.js";

describe("ModuleApiRegistry", () => {
  it("resolves a registered module's client", () => {
    const registry = new ModuleApiRegistry();
    const api = { listContacts: async () => [] };
    registry.register("contacts", api);
    expect(registry.resolve("contacts")).toBe(api);
  });

  it("returns undefined for a module with no registered client", () => {
    expect(new ModuleApiRegistry().resolve("missing")).toBeUndefined();
  });

  it("replaces a module's prior client and notifies subscribers", () => {
    const registry = new ModuleApiRegistry();
    registry.register("contacts", {});
    const listener = vi.fn();
    registry.subscribe(listener);
    const replacement = {};
    registry.register("contacts", replacement);
    expect(registry.resolve("contacts")).toBe(replacement);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("clears a module's prior client when re-registered with undefined", () => {
    const registry = new ModuleApiRegistry();
    registry.register("contacts", {});
    registry.register("contacts", undefined);
    expect(registry.resolve("contacts")).toBeUndefined();
  });

  it("doesn't notify when the same client is registered again", () => {
    const registry = new ModuleApiRegistry();
    const api = {};
    registry.register("contacts", api);
    const listener = vi.fn();
    registry.subscribe(listener);
    registry.register("contacts", api);
    registry.register("missing", undefined);
    expect(listener).not.toHaveBeenCalled();
  });

  it("stops notifying after unsubscribe", () => {
    const registry = new ModuleApiRegistry();
    const listener = vi.fn();
    registry.subscribe(listener)();
    registry.register("contacts", {});
    expect(listener).not.toHaveBeenCalled();
  });
});
