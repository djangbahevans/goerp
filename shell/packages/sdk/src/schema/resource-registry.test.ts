import { describe, expect, it, vi } from "vitest";
import { buildResourceRegistry, ResourceRegistry } from "./resource-registry.js";
import type { MetaSchema } from "./types.js";

function route(overrides: Partial<MetaSchema["modules"][string]["routes"][number]>) {
  return {
    method: "GET",
    path: "",
    permissions: [],
    response_is_list: false,
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
      models: {},
      permissions: [],
      frontend: null,
      public_config: {},
      routes: [
        route({ method: "GET", path: "/contacts", model: "contacts.contact", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/{id}", model: "contacts.contact", crud_action: "get" }),
        route({ method: "POST", path: "/contacts", model: "contacts.contact", crud_action: "create" }),
        route({ method: "PUT", path: "/contacts/{id}", model: "contacts.contact", crud_action: "update" }),
        // No delete route for contacts.contact.
        route({ method: "GET", path: "/contacts/tags", model: "contacts.tag", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/tags/{id}", model: "contacts.tag", crud_action: "get" }),
        // A named action route — no model/crud_action, must be skipped entirely.
        route({ method: "POST", path: "/contacts/{id}/merge", name: "merge" }),
        // Only a create route, no list or get — must be skipped (no usable routes).
        route({ method: "POST", path: "/contacts/import", model: "contacts.import_job", crud_action: "create" }),
      ],
    },
    sales: {
      name: "sales",
      version: "1.0.0",
      display_name: "Sales",
      views: [],
      navigation: [],
      models: {},
      permissions: [],
      frontend: null,
      public_config: {},
      routes: [
        route({ method: "GET", path: "/orders", model: "sales.order", crud_action: "list" }),
        route({ method: "GET", path: "/orders/{id}", model: "sales.order", crud_action: "get" }),
        route({ method: "POST", path: "/orders", model: "sales.order", crud_action: "create" }),
        route({ method: "PUT", path: "/orders/{id}", model: "sales.order", crud_action: "update" }),
        route({ method: "DELETE", path: "/orders/{id}", model: "sales.order", crud_action: "delete" }),
      ],
    },
  },
};

describe("buildResourceRegistry", () => {
  it("resolves a model's full CRUD paths", () => {
    const registry = buildResourceRegistry(schema);

    expect(registry.get("sales.order")).toEqual({
      module: "sales",
      resource: "sales.order",
      listPath: "/orders",
      getPath: "/orders/{id}",
      createPath: "/orders",
      updatePath: "/orders/{id}",
      deletePath: "/orders/{id}",
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: "DELETE",
    });
  });

  it("sets deletePath/deleteMethod to null when no delete route is declared", () => {
    const registry = buildResourceRegistry(schema);
    const entry = registry.get("contacts.contact");

    expect(entry?.deletePath).toBeNull();
    expect(entry?.deleteMethod).toBeNull();
  });

  it("resolves a model with only list+get routes", () => {
    const registry = buildResourceRegistry(schema);

    expect(registry.get("contacts.tag")).toEqual({
      module: "contacts",
      resource: "contacts.tag",
      listPath: "/contacts/tags",
      getPath: "/contacts/tags/{id}",
      createPath: "",
      updatePath: "",
      deletePath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
    });
  });

  it("skips a model with neither list nor get routes", () => {
    const registry = buildResourceRegistry(schema);
    expect(registry.has("contacts.import_job")).toBe(false);
  });

  it("skips routes with no model/crud_action set", () => {
    const registry = buildResourceRegistry(schema);
    // The merge action route has no model — asserting the registry has
    // exactly the models expected confirms it contributed no stray entry.
    expect([...registry.keys()].sort()).toEqual(["contacts.contact", "contacts.tag", "sales.order"]);
  });

  it("resolves models across different modules without collision", () => {
    const registry = buildResourceRegistry(schema);
    expect(registry.get("contacts.contact")?.module).toBe("contacts");
    expect(registry.get("sales.order")?.module).toBe("sales");
  });
});

describe("ResourceRegistry", () => {
  function fakeSchema() {
    return { getSchema: vi.fn(async () => schema) };
  }

  it("resolves a known resource", async () => {
    const registry = new ResourceRegistry(fakeSchema());
    await expect(registry.resolve("sales.order")).resolves.toMatchObject({ listPath: "/orders" });
  });

  it("rejects an unknown resource", async () => {
    const registry = new ResourceRegistry(fakeSchema());
    await expect(registry.resolve("nope.nothing")).rejects.toThrow(/unknown resource/);
  });

  it("caches the built registry across multiple resolve() calls", async () => {
    const schemaSource = fakeSchema();
    const registry = new ResourceRegistry(schemaSource);

    await registry.resolve("sales.order");
    await registry.resolve("contacts.contact");

    expect(schemaSource.getSchema).toHaveBeenCalledTimes(1);
  });

  it("retries after a failed attempt instead of caching the rejection", async () => {
    const schemaSource = { getSchema: vi.fn() };
    schemaSource.getSchema.mockRejectedValueOnce(new Error("network down"));
    schemaSource.getSchema.mockResolvedValueOnce(schema);
    const registry = new ResourceRegistry(schemaSource);

    await expect(registry.resolve("sales.order")).rejects.toThrow("network down");
    await expect(registry.resolve("sales.order")).resolves.toMatchObject({ listPath: "/orders" });
    expect(schemaSource.getSchema).toHaveBeenCalledTimes(2);
  });
});
