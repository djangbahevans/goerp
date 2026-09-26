import { describe, expect, it, vi } from "vitest";
import { resolveRecordViewPath, resolveViewPath, ViewPathRegistry } from "./resolve-view-path.js";
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
          { method: "GET", path: "/contacts", permissions: [], response_is_list: true, view: "contacts_list" },
          { method: "GET", path: "/contacts/new", permissions: [], response_is_list: false, view: "contacts_form" },
        ],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        view_extensions: [],
        view_extension_definitions: [],
        load_order: 0,
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
        routes: [{ method: "GET", path: "/orders/new", permissions: [], response_is_list: false, view: "orders_form" }],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        view_extensions: [],
        view_extension_definitions: [],
        load_order: 0,
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

describe("resolveRecordViewPath", () => {
  // The routes EnableOps(List, Get, Create, Update) + EnableViews(ListView,
  // FormView) generate: the form view serves both create and one record.
  const generated = schemaWith({
    crm: {
      name: "crm",
      version: "1",
      display_name: "CRM",
      routes: [
        { method: "GET", path: "/crm/contacts", permissions: [], response_is_list: true, view: "crm_contact_list" },
        { method: "POST", path: "/crm/contacts", permissions: [], response_is_list: false, view: "crm_contact_form" },
        {
          method: "PUT",
          path: "/crm/contacts/{id}",
          permissions: [],
          response_is_list: false,
          view: "crm_contact_form",
        },
        {
          method: "GET",
          path: "/crm/contacts/{id}",
          permissions: [],
          response_is_list: false,
          view: "crm_contact_form",
        },
      ],
      views: [],
      navigation: [],
      models: {},
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      public_config: {},
    },
  });

  it("picks the view's GET route with an {id}, not its create route", () => {
    expect(resolveViewPath(generated, "crm_contact_form", "crm")).toBe("/crm/contacts");
    expect(resolveRecordViewPath(generated, "crm_contact_form", "crm")).toBe("/crm/contacts/{id}");
    expect(resolveRecordViewPath(generated, "crm.crm_contact_form", "other")).toBe("/crm/contacts/{id}");
  });

  it("returns null when no route of the view names a record", () => {
    expect(resolveRecordViewPath(generated, "crm_contact_list", "crm")).toBe(null);
    expect(resolveRecordViewPath(generated, "missing", "crm")).toBe(null);
    expect(resolveRecordViewPath(schemaWith({}), "crm_contact_form", "crm")).toBe(null);
  });

  it("resolves through ViewPathRegistry.resolveRecord", async () => {
    const registry = new ViewPathRegistry({ getSchema: vi.fn(async () => generated) });
    expect(await registry.resolveRecord("crm_contact_form", "crm")).toBe("/crm/contacts/{id}");
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
          { method: "GET", path: "/contacts/new", permissions: [], response_is_list: false, view: "contacts_form" },
        ],
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        view_extensions: [],
        view_extension_definitions: [],
        load_order: 0,
        public_config: {},
      },
    });
    const registry = new ViewPathRegistry({ getSchema: vi.fn(async () => schema) });

    expect(await registry.resolve("contacts_form", "contacts")).toBe("/contacts/new");
  });
});
