import { describe, expect, it, vi } from "vitest";
import type { APIClient } from "../http/types.js";
import { SchemaRegistry } from "./schema-registry.js";

function fakeClient(schema: unknown): Pick<APIClient, "get"> {
  return { get: vi.fn(async () => schema) as Pick<APIClient, "get">["get"] };
}

const schema = { modules: {}, engine_version: "test", schema_hash: "abc" };

describe("SchemaRegistry", () => {
  it("fetches the schema", async () => {
    const registry = new SchemaRegistry(fakeClient(schema));
    await expect(registry.getSchema()).resolves.toEqual(schema);
  });

  it("caches the schema fetch across multiple getSchema() calls", async () => {
    const client = fakeClient(schema);
    const registry = new SchemaRegistry(client);

    await registry.getSchema();
    await registry.getSchema();

    expect(client.get).toHaveBeenCalledTimes(1);
  });

  it("retries the fetch after a failed attempt instead of caching the rejection", async () => {
    const client = { get: vi.fn() };
    client.get.mockRejectedValueOnce(new Error("network down"));
    client.get.mockResolvedValueOnce(schema);
    const registry = new SchemaRegistry(client);

    await expect(registry.getSchema()).rejects.toThrow("network down");
    await expect(registry.getSchema()).resolves.toEqual(schema);
    expect(client.get).toHaveBeenCalledTimes(2);
  });

  it("requests GET /_meta/schema", async () => {
    const client = fakeClient(schema);
    const registry = new SchemaRegistry(client);

    await registry.getSchema();

    expect(client.get).toHaveBeenCalledWith("/_meta/schema");
  });
});
