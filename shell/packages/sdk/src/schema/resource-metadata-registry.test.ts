import { describe, expect, it, vi } from "vitest";
import type { ResourceMetadataEntry } from "./resource-metadata-registry.js";
import {
  buildResourceMetadataRegistry,
  ResourceMetadataRegistry,
  resourceListPath,
} from "./resource-metadata-registry.js";
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

function route(overrides: Partial<MetaSchema["modules"][string]["routes"][number]>) {
  return {
    method: "GET",
    path: "",
    permissions: [],
    response_is_list: false,
    ...overrides,
  };
}

function view(overrides: Record<string, unknown>) {
  return { name: "", type: "list", resource: "", label: "", ...overrides };
}

function navGroup(children: Record<string, unknown>[], overrides: Record<string, unknown> = {}) {
  return { label: "", order: 0, children, ...overrides };
}

const schema: MetaSchema = {
  engine_version: "test",
  schema_hash: "abc",
  modules: {
    contacts: {
      name: "contacts",
      version: "1.0.0",
      display_name: "Contacts",
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      public_config: {},
      routes: [
        route({ method: "GET", path: "/contacts", model: "contacts.contact", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/{id}", model: "contacts.contact", crud_action: "get" }),
        route({ method: "GET", path: "/contacts/tags", model: "contacts.tag", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/tags/{id}", model: "contacts.tag", crud_action: "get" }),
        // Has a list route but no view and no nav item at all — a valid
        // relation-picker target per view-system.md's "Label field when
        // there's no list view at all".
        route({ method: "GET", path: "/contacts/leads", model: "contacts.lead", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/imports", model: "contacts.import_job", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/sessions", model: "contacts.session", crud_action: "list" }),
      ],
      views: [
        view({
          name: "contacts_list",
          type: "list",
          resource: "contacts.contact",
          columns: [
            { field: "email", primary: false },
            { field: "display_name", primary: true },
          ],
        }),
        view({ name: "contacts_form", type: "form", resource: "contacts.contact" }),
        // A second list view for the same resource — the first one found wins.
        view({ name: "contacts_list_alt", type: "list", resource: "contacts.contact" }),
        // Explicit label_field, takes priority over the primary column.
        // Explicit search_param, overriding the "q" default.
        view({
          name: "tags_list",
          type: "list",
          resource: "contacts.tag",
          label_field: "slug",
          search_param: "search",
        }),
        // No primary column, label_field, or .Primary() field — falls
        // through to the "name" convention fallback.
        view({ name: "imports_list", type: "list", resource: "contacts.import_job" }),
        // No primary column, label_field, .Primary() field, or
        // display_name/name/title field — falls all the way back to "id".
        view({ name: "sessions_list", type: "list", resource: "contacts.session" }),
      ],
      navigation: [
        navGroup([{ label: "Contacts", view: "contacts_list", route: "/contacts" }]),
        navGroup([{ label: "Imports", view: "imports_list", route: "/imports" }]),
        navGroup([{ label: "Sessions", view: "sessions_list", route: "/sessions" }]),
        // Hidden behind a system-only permission — still registers the resource.
        navGroup([{ label: "Tags", view: "tags_list", route: "/tags" }], {
          label: "System",
          permission: "system:admin",
        }),
      ],
      models: {
        "contacts.contact": model({
          name: "contact",
          fields: [
            { name: "display_name", type: "text" },
            { name: "email", type: "text", is_primary: true },
          ],
        }),
        "contacts.tag": model({
          name: "tag",
          label: "Tag",
          fields: [
            { name: "slug", type: "text" },
            // .Primary() field, distinct from the view's explicit
            // label_field ("slug") — proves label_field still wins.
            { name: "internal_code", type: "text", is_primary: true },
          ],
        }),
        "contacts.import_job": model({
          name: "import_job",
          fields: [
            { name: "id", type: "text" },
            { name: "name", type: "text" },
          ],
        }),
        "contacts.session": model({
          name: "session",
          fields: [
            { name: "id", type: "text" },
            { name: "token", type: "text" },
          ],
        }),
        "contacts.audit_log": model({ name: "audit_log", fields: [{ name: "id", type: "text" }] }),
        "contacts.lead": model({
          name: "lead",
          fields: [
            { name: "id", type: "text" },
            { name: "full_name", type: "text", is_primary: true },
          ],
        }),
      },
    },
  },
};

describe("buildResourceMetadataRegistry", () => {
  it("resolves routes, default views, labelField, searchParam, and fields for a registered resource", () => {
    const registry = buildResourceMetadataRegistry(schema);

    expect(registry.get("contacts.tag")).toEqual({
      module: "contacts",
      resource: "contacts.tag",
      listRoute: "GET /contacts/tags",
      getRoute: "GET /contacts/tags/{id}",
      defaultListView: "tags_list",
      defaultFormView: "",
      labelField: "slug",
      searchParam: "search",
      fields: [
        { name: "slug", type: "text" },
        { name: "internal_code", type: "text", is_primary: true },
      ],
    });
  });

  it("resolves labelField from the primary:true column when no label_field is declared", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.contact")?.labelField).toBe("display_name");
  });

  it('falls back to the default searchParam of "q" when the list view declares none', () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.contact")?.searchParam).toBe("q");
  });

  it("resolves searchParam from the default list view's search_param when declared", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.tag")?.searchParam).toBe("search");
  });

  it("prefers the list view's primary:true column over the model's .Primary() field", () => {
    // contacts.contact's "email" field is marked .Primary(), but its list
    // view's "display_name" column is primary:true — the column wins.
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.contact")?.labelField).toBe("display_name");
  });

  it("prefers an explicit label_field over the model's .Primary() field", () => {
    // contacts.tag's "internal_code" field is marked .Primary(), but its
    // list view declares label_field: "slug" — the explicit field wins.
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.tag")?.labelField).toBe("slug");
  });

  it("picks the first list/form view declared for a resource", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.contact")?.defaultListView).toBe("contacts_list");
    expect(registry.get("contacts.contact")?.defaultFormView).toBe("contacts_form");
  });

  it("falls back to the display_name/name/title convention when no primary column or label_field exists", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.import_job")?.labelField).toBe("name");
  });

  it("falls back to id when nothing else resolves", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.session")?.labelField).toBe("id");
  });

  it("excludes a resource with no list route", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.has("contacts.audit_log")).toBe(false);
  });

  it("still registers a resource whose only nav item sits behind a system-only permission", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.has("contacts.tag")).toBe(true);
  });

  it("registers a resource with a list route but no view and no NavItem", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.lead")).toMatchObject({
      listRoute: "GET /contacts/leads",
      defaultListView: "",
      defaultFormView: "",
    });
  });

  it("resolves labelField from the model's .Primary() field when there's no list view at all", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.lead")?.labelField).toBe("full_name");
  });

  it('falls back to the default searchParam of "q" when there\'s no list view at all', () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.lead")?.searchParam).toBe("q");
  });
});

describe("ResourceMetadataRegistry", () => {
  function fakeSchema() {
    return { getSchema: vi.fn(async () => schema) };
  }

  it("resolves a registered resource", async () => {
    const registry = new ResourceMetadataRegistry(fakeSchema());
    await expect(registry.resolve("contacts.contact")).resolves.toMatchObject({ labelField: "display_name" });
  });

  it("resolves undefined for a resource with no list route", async () => {
    const registry = new ResourceMetadataRegistry(fakeSchema());
    await expect(registry.resolve("contacts.audit_log")).resolves.toBeUndefined();
  });

  it("caches the built registry across multiple resolve() calls", async () => {
    const schemaSource = fakeSchema();
    const registry = new ResourceMetadataRegistry(schemaSource);

    await registry.resolve("contacts.contact");
    await registry.resolve("contacts.tag");

    expect(schemaSource.getSchema).toHaveBeenCalledTimes(1);
  });
});

describe("resourceListPath", () => {
  function entry(listRoute: string): ResourceMetadataEntry {
    return {
      module: "contacts",
      resource: "contacts.contact",
      listRoute,
      getRoute: "",
      defaultListView: "",
      defaultFormView: "",
      labelField: "display_name",
      searchParam: "q",
      fields: [],
    };
  }

  it("strips a GET prefix", () => {
    expect(resourceListPath(entry("GET /contacts"))).toBe("/contacts");
  });

  it("strips whatever method is actually present, not just GET", () => {
    expect(resourceListPath(entry("POST /contacts/search"))).toBe("/contacts/search");
  });

  it("returns undefined for a resource with no matching list route", () => {
    expect(resourceListPath(entry(""))).toBeUndefined();
  });
});
