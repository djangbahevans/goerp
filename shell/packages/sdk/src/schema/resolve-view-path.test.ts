import { describe, expect, it, vi } from "vitest";
import { resolveViewPath, ViewPathRegistry } from "./resolve-view-path.js";
import type { MetaSchema } from "./types.js";

function schemaWith(modules: MetaSchema["modules"]): MetaSchema {
  return { modules, engine_version: "1", schema_hash: "h" };
}

describe("resolveViewPath", () => {
  it("resolves an unqualified view name against the current module", () => {
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [
          { method: "GET", path: "/contacts", permissions: null, response_is_list: true, view: "contacts_list" },
          { method: "GET", path: "/contacts/new", permissions: null, response_is_list: false, view: "contacts_form" },
        ],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewPath(schema, "contacts_form", "contacts")).toBe("/contacts/new");
  });

  it("resolves a module-qualified view name against the named module, not the current one", () => {
    const schema = schemaWith({
      sales: {
        name: "sales",
        version: "1",
        display_name: "Sales",
        routes: [
          { method: "GET", path: "/orders/new", permissions: null, response_is_list: false, view: "orders_form" },
        ],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewPath(schema, "sales.orders_form", "contacts")).toBe("/orders/new");
  });

  it("returns null for an unknown module or an unmatched view name", () => {
    const schema = schemaWith({});
    expect(resolveViewPath(schema, "contacts_form", "contacts")).toBe(null);
    expect(resolveViewPath(schema, "missing.form", "contacts")).toBe(null);
  });
});

describe("ViewPathRegistry", () => {
  it("resolves through the injected schema source", async () => {
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [
          { method: "GET", path: "/contacts/new", permissions: null, response_is_list: false, view: "contacts_form" },
        ],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });
    const registry = new ViewPathRegistry({ getSchema: vi.fn(async () => schema) });

    expect(await registry.resolve("contacts_form", "contacts")).toBe("/contacts/new");
  });
});
