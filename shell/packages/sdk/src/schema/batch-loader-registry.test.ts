import { describe, expect, it, vi } from "vitest";
import { BatchLoaderRegistry } from "./batch-loader-registry.js";

const KEY = "sales.orders_list.customer_id";

describe("BatchLoaderRegistry", () => {
  it("resolves a registered loader by its view.field key", async () => {
    const registry = new BatchLoaderRegistry();
    const loader = vi.fn(async (ids: string[]) => new Map(ids.map((id) => [id, `Label ${id}`])));
    registry.register(KEY, loader);

    expect(registry.resolve(KEY)).toBe(loader);
  });

  it("throws for a key with no registered loader", () => {
    const registry = new BatchLoaderRegistry();
    expect(() => registry.resolve(KEY)).toThrow(/no batch loader registered for/);
  });

  it("throws on a key collision instead of silently overwriting", () => {
    const registry = new BatchLoaderRegistry();
    const first = vi.fn(async () => new Map());
    const second = vi.fn(async () => new Map());
    registry.register(KEY, first);

    expect(() => registry.register(KEY, second)).toThrow(/already has a registered batch loader/);
    expect(registry.resolve(KEY)).toBe(first);
  });

  it("has() reports registration without throwing", () => {
    const registry = new BatchLoaderRegistry();
    expect(registry.has(KEY)).toBe(false);
    registry.register(
      KEY,
      vi.fn(async () => new Map()),
    );
    expect(registry.has(KEY)).toBe(true);
  });
});
