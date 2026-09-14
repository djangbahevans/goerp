import { afterEach, describe, expect, it, vi } from "vitest";
import { resolveViewDeclaration, ViewDeclarationRegistry } from "./resolve-view-declaration.js";
import type { MetaSchema } from "./types.js";

afterEach(() => {
  vi.restoreAllMocks();
});

function schemaWith(modules: MetaSchema["modules"]): MetaSchema {
  return { modules, engine_version: "1", schema_hash: "h" };
}

const contactsListView = { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" };

describe("resolveViewDeclaration", () => {
  it("resolves an unqualified view name against the current module", () => {
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [],
        views: [contactsListView],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewDeclaration(schema, "contacts_list", "contacts")).toEqual(contactsListView);
  });

  it("resolves a module-qualified view name against the named module, not the current one", () => {
    const schema = schemaWith({
      sales: {
        name: "sales",
        version: "1",
        display_name: "Sales",
        routes: [],
        views: [{ name: "orders_kanban", type: "kanban", resource: "sales.order", label: "Orders" }],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewDeclaration(schema, "sales.orders_kanban", "contacts")?.type).toBe("kanban");
  });

  it("returns null for an unknown module, an unmatched view name, or a malformed entry", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [],
        views: ["not-an-object", { name: "contacts_list" /* no type */ }],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewDeclaration(schema, "missing.view", "contacts")).toBe(null);
    expect(resolveViewDeclaration(schema, "contacts_list", "contacts")).toBe(null);
    expect(resolveViewDeclaration(schemaWith({}), "contacts_list", "contacts")).toBe(null);
  });

  it("warns (rather than silently vanishing) for an entry found by name but missing a common field", () => {
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [],
        views: [{ name: "broken_view", type: "list" }],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewDeclaration(schema, "broken_view", "contacts")).toBe(null);
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("broken_view"));
  });

  it("keeps a view type's own extra fields intact alongside the validated common ones", () => {
    const schema = schemaWith({
      crm: {
        name: "crm",
        version: "1",
        display_name: "CRM",
        routes: [],
        views: [{ name: "leads_kanban", type: "kanban", resource: "crm.lead", label: "Leads", group_by: "stage" }],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });

    expect(resolveViewDeclaration(schema, "leads_kanban", "crm")).toMatchObject({ group_by: "stage" });
  });
});

describe("ViewDeclarationRegistry", () => {
  it("resolves through the injected schema source", async () => {
    const schema = schemaWith({
      contacts: {
        name: "contacts",
        version: "1",
        display_name: "Contacts",
        routes: [],
        views: [contactsListView],
        navigation: [],
        models: {},
        permissions: [],
        frontend: null,
        public_config: {},
      },
    });
    const registry = new ViewDeclarationRegistry({ getSchema: vi.fn(async () => schema) });

    await expect(registry.resolve("contacts_list", "contacts")).resolves.toEqual(contactsListView);
  });
});
