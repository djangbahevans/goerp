import { describe, expect, it } from "vitest";
import { buildModelRegistry, ModelRegistry } from "./model-registry.js";
import type { MetaSchema, ModelDef } from "./types.js";

function model(overrides: Partial<ModelDef>): ModelDef {
  return {
    name: "contact",
    label: "Contact",
    label_plural: "Contacts",
    fields: [],
    enabled_ops: [],
    shareable: false,
    ...overrides,
  };
}

const schema: MetaSchema = {
  engine_version: "test",
  schema_hash: "abc",
  modules: {
    contacts: {
      name: "contacts",
      version: "1.0.0",
      display_name: "Contacts",
      views: [],
      navigation: [],
      permissions: [],
      frontend: null,
      public_config: {},
      routes: [],
      models: {
        "contacts.contact": model({ name: "contact", shareable: true }),
        "contacts.tag": model({ name: "tag", label: "Tag", label_plural: "Tags" }),
      },
    },
    sales: {
      name: "sales",
      version: "1.0.0",
      display_name: "Sales",
      views: [],
      navigation: [],
      permissions: [],
      frontend: null,
      public_config: {},
      routes: [],
      models: {
        "sales.order": model({ name: "order", label: "Order", label_plural: "Orders" }),
      },
    },
  },
};

describe("buildModelRegistry", () => {
  it("merges every module's already-qualified models map into one flat registry", () => {
    const registry = buildModelRegistry(schema);
    expect(registry.get("contacts.contact")?.shareable).toBe(true);
    expect(registry.get("contacts.tag")?.label).toBe("Tag");
    expect(registry.get("sales.order")?.label).toBe("Order");
    expect(registry.size).toBe(3);
  });
});

describe("ModelRegistry", () => {
  it("resolves a known resource", async () => {
    const registry = new ModelRegistry({ getSchema: () => Promise.resolve(schema) });
    await expect(registry.resolve("contacts.contact")).resolves.toMatchObject({ shareable: true });
  });

  it("throws for an unknown resource", async () => {
    const registry = new ModelRegistry({ getSchema: () => Promise.resolve(schema) });
    await expect(registry.resolve("contacts.missing")).rejects.toThrow(/unknown resource/);
  });
});
