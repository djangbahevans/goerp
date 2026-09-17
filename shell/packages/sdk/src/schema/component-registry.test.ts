import { describe, expect, it } from "vitest";
import { ComponentRegistry } from "./component-registry.js";

function Stub() {
  return null;
}

describe("ComponentRegistry", () => {
  it("resolves a registered component by name", () => {
    const registry = new ComponentRegistry();
    registry.register("BulkTagAction", Stub);
    expect(registry.resolve("BulkTagAction")).toBe(Stub);
  });

  it("throws on an unregistered name", () => {
    const registry = new ComponentRegistry();
    expect(() => registry.resolve("Missing")).toThrow(/unknown component "Missing"/);
  });

  it("throws on a name collision instead of silently overwriting", () => {
    const registry = new ComponentRegistry();
    registry.register("BulkTagAction", Stub);
    function Other() {
      return null;
    }
    expect(() => registry.register("BulkTagAction", Other)).toThrow(/"BulkTagAction" is already registered/);
    expect(registry.resolve("BulkTagAction")).toBe(Stub);
  });

  it("has() reports registration without throwing", () => {
    const registry = new ComponentRegistry();
    expect(registry.has("BulkTagAction")).toBe(false);
    registry.register("BulkTagAction", Stub);
    expect(registry.has("BulkTagAction")).toBe(true);
  });

  it("unregister() clears a name so it can be registered again without colliding", () => {
    const registry = new ComponentRegistry();
    registry.register("BulkTagAction", Stub);
    registry.unregister("BulkTagAction");
    function Other() {
      return null;
    }
    expect(() => registry.register("BulkTagAction", Other)).not.toThrow();
    expect(registry.resolve("BulkTagAction")).toBe(Other);
  });

  describe("tryResolve()", () => {
    it("returns the registered component for a resolvable name", () => {
      const registry = new ComponentRegistry();
      registry.register("BulkTagAction", Stub);
      expect(registry.tryResolve("BulkTagAction")).toBe(Stub);
    });

    it("returns undefined, not a throw, for an unregistered name", () => {
      const registry = new ComponentRegistry();
      expect(registry.tryResolve("Missing")).toBeUndefined();
    });

    it("returns undefined for an undefined name, without a Map lookup", () => {
      const registry = new ComponentRegistry();
      expect(registry.tryResolve(undefined)).toBeUndefined();
    });
  });
});
