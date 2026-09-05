import { describe, expect, it, vi } from "vitest";
import type { APIClient } from "../http/types.js";
import { ActionRegistry } from "./action-registry.js";

function fakeClient(schema: unknown): Pick<APIClient, "get"> {
  return { get: vi.fn(async () => schema) as Pick<APIClient, "get">["get"] };
}

const schema = {
  modules: {
    sales: {
      routes: [
        { method: "POST", path: "/orders/{id}/confirm", name: "confirm" },
        { method: "GET", path: "/orders", name: null, crud_action: "list" },
      ],
    },
  },
};

describe("ActionRegistry", () => {
  it("resolves a known action route", async () => {
    const client = fakeClient(schema);
    const registry = new ActionRegistry(client);

    await expect(registry.resolve("sales.confirm")).resolves.toEqual({
      method: "POST",
      path: "/orders/{id}/confirm",
    });
  });

  it("caches the schema fetch across multiple resolve() calls", async () => {
    const client = fakeClient(schema);
    const registry = new ActionRegistry(client);

    await registry.resolve("sales.confirm");
    await registry.resolve("sales.confirm");

    expect(client.get).toHaveBeenCalledTimes(1);
  });

  it("rejects a route name with no module separator", async () => {
    const registry = new ActionRegistry(fakeClient(schema));
    await expect(registry.resolve("confirm")).rejects.toThrow(/must be in/);
  });

  it("rejects an unknown module", async () => {
    const registry = new ActionRegistry(fakeClient(schema));
    await expect(registry.resolve("unknown.confirm")).rejects.toThrow(/unknown module/);
  });

  it("rejects an unknown action name within a known module", async () => {
    const registry = new ActionRegistry(fakeClient(schema));
    await expect(registry.resolve("sales.doesNotExist")).rejects.toThrow(/no action named/);
  });

  it("retries the schema fetch after a failed attempt instead of caching the rejection", async () => {
    const client = { get: vi.fn() };
    client.get.mockRejectedValueOnce(new Error("network down"));
    client.get.mockResolvedValueOnce(schema);
    const registry = new ActionRegistry(client);

    await expect(registry.resolve("sales.confirm")).rejects.toThrow("network down");
    await expect(registry.resolve("sales.confirm")).resolves.toEqual({
      method: "POST",
      path: "/orders/{id}/confirm",
    });
    expect(client.get).toHaveBeenCalledTimes(2);
  });
});
