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

  it("has() reports registration without throwing", () => {
    const registry = new ComponentRegistry();
    expect(registry.has("BulkTagAction")).toBe(false);
    registry.register("BulkTagAction", Stub);
    expect(registry.has("BulkTagAction")).toBe(true);
  });
});
