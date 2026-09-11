import { describe, expect, it, vi } from "vitest";
import { buildResourceMetadataRegistry, ResourceMetadataRegistry } from "./resource-metadata-registry.js";
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
  return { name: "", type: "list", resource: "", ...overrides };
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
      public_config: {},
      routes: [
        route({ method: "GET", path: "/contacts", model: "contacts.contact", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/{id}", model: "contacts.contact", crud_action: "get" }),
        route({ method: "GET", path: "/contacts/tags", model: "contacts.tag", crud_action: "list" }),
        route({ method: "GET", path: "/contacts/tags/{id}", model: "contacts.tag", crud_action: "get" }),
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
        view({ name: "tags_list", type: "list", resource: "contacts.tag", label_field: "slug" }),
        // Registered via navigation, but has no primary column or explicit
        // label_field — falls through to the "name" convention fallback.
        view({ name: "imports_list", type: "list", resource: "contacts.import_job" }),
        // Registered via navigation, but no primary column, label_field, or
        // display_name/name/title field — falls all the way back to "id".
        view({ name: "sessions_list", type: "list", resource: "contacts.session" }),
        // Never referenced by any nav item — must be absent from the registry.
        view({ name: "audit_list", type: "list", resource: "contacts.audit_log" }),
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
            { name: "email", type: "text" },
          ],
        }),
        "contacts.tag": model({ name: "tag", label: "Tag", fields: [{ name: "slug", type: "text" }] }),
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
      },
    },
  },
};

describe("buildResourceMetadataRegistry", () => {
  it("resolves routes, default views, labelField (explicit label_field), and fields for a registered resource", () => {
    const registry = buildResourceMetadataRegistry(schema);

    expect(registry.get("contacts.tag")).toEqual({
      module: "contacts",
      resource: "contacts.tag",
      listRoute: "GET /contacts/tags",
      getRoute: "GET /contacts/tags/{id}",
      defaultListView: "tags_list",
      defaultFormView: "",
      labelField: "slug",
      searchParam: "q",
      fields: [{ name: "slug", type: "text" }],
    });
  });

  it("resolves labelField from the primary:true column when no label_field is declared", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.get("contacts.contact")?.labelField).toBe("display_name");
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

  it("excludes a resource with no NavItem referencing any of its views", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.has("contacts.audit_log")).toBe(false);
  });

  it("still registers a resource whose only nav item sits behind a system-only permission", () => {
    const registry = buildResourceMetadataRegistry(schema);
    expect(registry.has("contacts.tag")).toBe(true);
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

  it("resolves undefined for a resource with no nav-item registration", async () => {
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
