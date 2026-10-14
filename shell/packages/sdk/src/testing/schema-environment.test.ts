import { describe, expect, it } from "vitest";
import { resourceRegistry, schemaRegistry } from "../schema/index.js";
import type { MetaSchema } from "../schema/types.js";
import { mountTestingSchema } from "./schema-environment.js";

const schema: MetaSchema = { modules: {}, engine_version: "testing", schema_hash: "one" };

describe("testing schema isolation", () => {
  it("restores original methods when identical schemas unmount out of order", async () => {
    const originalSchema = schemaRegistry.getSchema;
    const originalResolve = resourceRegistry.resolve;
    const first = mountTestingSchema(schema);
    const second = mountTestingSchema(schema);
    try {
      first();
      expect(await schemaRegistry.getSchema()).toBe(schema);
      expect(resourceRegistry.resolve).not.toBe(originalResolve);
    } finally {
      first();
      second();
    }
    expect(schemaRegistry.getSchema).toBe(originalSchema);
    expect(resourceRegistry.resolve).toBe(originalResolve);
  });

  it("a provider without a schema temporarily restores the original registries", async () => {
    const original = schemaRegistry.getSchema;
    const outer = mountTestingSchema(schema);
    const inner = mountTestingSchema();
    try {
      expect(schemaRegistry.getSchema).toBe(original);
      inner();
      expect(await schemaRegistry.getSchema()).toBe(schema);
    } finally {
      inner();
      outer();
    }
    expect(schemaRegistry.getSchema).toBe(original);
  });
});
