import { describe, expect, it } from "vitest";
import { ExtensionBatchLoaderRegistry } from "./extension-batch-loader-registry.js";

describe("ExtensionBatchLoaderRegistry", () => {
  it("resolves a registered loader by key", () => {
    const registry = new ExtensionBatchLoaderRegistry();
    const loader = async (ids: string[]) => new Map(ids.map((id) => [id, { "contacts:customer_name": id }]));
    registry.register("sales.orders_list", loader);
    expect(registry.resolve("sales.orders_list")).toBe(loader);
  });

  it("throws on an unregistered key", () => {
    const registry = new ExtensionBatchLoaderRegistry();
    expect(() => registry.resolve("missing.view")).toThrow(/no batch loader registered for "missing.view"/);
  });

  it("throws on a key collision instead of silently overwriting", () => {
    const registry = new ExtensionBatchLoaderRegistry();
    const first = async () => new Map();
    registry.register("sales.orders_list", first);
    expect(() => registry.register("sales.orders_list", async () => new Map())).toThrow(
      /"sales.orders_list" already has a registered batch loader/,
    );
    expect(registry.resolve("sales.orders_list")).toBe(first);
  });

  it("has() reports registration without throwing", () => {
    const registry = new ExtensionBatchLoaderRegistry();
    expect(registry.has("sales.orders_list")).toBe(false);
    registry.register("sales.orders_list", async () => new Map());
    expect(registry.has("sales.orders_list")).toBe(true);
  });

  it("unregister() clears a key so it can be registered again without colliding", () => {
    const registry = new ExtensionBatchLoaderRegistry();
    registry.register("sales.orders_list", async () => new Map());
    registry.unregister("sales.orders_list");
    const replacement = async () => new Map();
    expect(() => registry.register("sales.orders_list", replacement)).not.toThrow();
    expect(registry.resolve("sales.orders_list")).toBe(replacement);
  });
});
