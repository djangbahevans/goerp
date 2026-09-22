import { describe, expect, it } from "vitest";
import type { MetaSchema } from "./types.js";
import { buildEmptyViewRegistry, buildViewRegistry, filterViewByCapability } from "./view-registry.js";

function route(overrides: Partial<MetaSchema["modules"][string]["routes"][number]>) {
  return {
    method: "GET",
    path: "",
    permissions: [],
    response_is_list: false,
    ...overrides,
  };
}

function moduleSchema(overrides: Partial<MetaSchema["modules"][string]>): MetaSchema["modules"][string] {
  return {
    name: "contacts",
    version: "1.0.0",
    display_name: "Contacts",
    views: [],
    navigation: [],
    models: {},
    permissions: [],
    frontend: null,
    view_extensions: [],
    view_extension_definitions: [],
    load_order: 0,
    public_config: {},
    routes: [],
    ...overrides,
  };
}

describe("buildViewRegistry — resolveRoute", () => {
  const schema: MetaSchema = {
    engine_version: "test",
    schema_hash: "abc",
    modules: {
      contacts: moduleSchema({
        name: "contacts",
        display_name: "Contacts",
        routes: [
          route({ method: "GET", path: "/contacts", model: "contacts.contact", crud_action: "list" }),
          route({ method: "GET", path: "/contacts", view: "contacts_list", permissions: ["contacts:contact:read"] }),
        ],
        views: [{ name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" }],
      }),
    },
  };

  it("resolves a route's already-expanded path to its view declaration", () => {
    const registry = buildViewRegistry(schema);
    const resolved = registry.resolveRoute("/contacts");

    expect(resolved).toMatchObject({
      module: "contacts",
      viewName: "contacts_list",
      viewType: "list",
      permissions: ["contacts:contact:read"],
      bundleUrl: null,
    });
  });

  it("returns null for a path nothing declares", () => {
    const registry = buildViewRegistry(schema);
    expect(registry.resolveRoute("/nonexistent")).toBeNull();
  });

  it("matches a concrete id against a declared {id}-templated route and captures the id", () => {
    const schemaWithForm: MetaSchema = {
      ...schema,
      modules: {
        contacts: moduleSchema({
          routes: [
            ...schema.modules.contacts!.routes,
            route({
              method: "GET",
              path: "/contacts/{id}",
              view: "contacts_form",
              permissions: ["contacts:contact:read"],
            }),
          ],
          views: [
            ...schema.modules.contacts!.views,
            { name: "contacts_form", type: "form", resource: "contacts.contact", label: "Contact" },
          ],
        }),
      },
    };
    const registry = buildViewRegistry(schemaWithForm);

    expect(registry.resolveRoute("/contacts/01j8x000000000000000000000")).toMatchObject({
      module: "contacts",
      viewName: "contacts_form",
      viewType: "form",
      recordId: "01j8x000000000000000000000",
    });
    // The template string itself is never a real navigation target, but the
    // raw-path exact-match entry still resolves it the same as before —
    // additive matching, not a replacement of the existing lookup.
    const exact = registry.resolveRoute("/contacts/{id}");
    expect(exact?.viewName).toBe("contacts_form");
    expect(exact?.recordId).toBeUndefined();
  });

  it("returns null when a concrete path's prefix doesn't match any declared {id}-templated route", () => {
    const registry = buildViewRegistry(schema);
    expect(registry.resolveRoute("/contacts/01j8x000000000000000000000")).toBeNull();
  });

  it("returns null when a route's view field doesn't match any declared view", () => {
    const schemaWithBadRef: MetaSchema = {
      ...schema,
      modules: {
        contacts: moduleSchema({
          routes: [route({ method: "GET", path: "/contacts", view: "not_a_real_view" })],
          views: [],
        }),
      },
    };
    expect(buildViewRegistry(schemaWithBadRef).resolveRoute("/contacts")).toBeNull();
  });

  it("exposes the resolved view's own permissions via viewPermissions(module.viewName)", () => {
    const registry = buildViewRegistry(schema);
    expect(registry.viewPermissions("contacts.contacts_list")).toEqual(["contacts:contact:read"]);
    expect(registry.viewPermissions("contacts.nope")).toEqual([]);
  });

  it("passes through bundle_url/bundle_sha256, null when the module has no custom frontend", () => {
    const withBundle: MetaSchema = {
      ...schema,
      modules: {
        contacts: {
          ...schema.modules.contacts!,
          frontend: { bundle_url: "https://cdn.example/contacts.js", bundle_sha256: "sha256:abc" },
        },
      },
    };
    const registry = buildViewRegistry(withBundle);
    expect(registry.getBundleUrl("contacts")).toBe("https://cdn.example/contacts.js");
    expect(registry.getBundleSHA256("contacts")).toBe("sha256:abc");
    expect(registry.getBundleUrl("unknown-module")).toBeNull();

    expect(buildViewRegistry(schema).getBundleUrl("contacts")).toBeNull();
  });

  it("reuses buildResourceRegistry/buildModelRegistry for resources/models rather than re-deriving them", () => {
    const schemaWithModel: MetaSchema = {
      ...schema,
      modules: {
        contacts: moduleSchema({
          routes: schema.modules.contacts!.routes,
          views: schema.modules.contacts!.views,
          models: {
            "contacts.contact": {
              name: "contacts.contact",
              label: "Contact",
              label_plural: "Contacts",
              fields: [],
              enabled_ops: ["list", "get"],
              shareable: false,
            },
          },
        }),
      },
    };
    const registry = buildViewRegistry(schemaWithModel);
    expect(registry.resources.get("contacts.contact")?.listPath).toBe("/contacts");
    expect(registry.models.get("contacts.contact")?.label).toBe("Contact");
  });
});

describe("buildViewRegistry — navigationTree", () => {
  it("expands a NavItem's relative route to /_m/{module}{route}, sorted by group order", () => {
    const schema: MetaSchema = {
      engine_version: "test",
      schema_hash: "abc",
      modules: {
        hr: moduleSchema({
          name: "hr",
          navigation: [
            {
              label: "HR",
              order: 5,
              children: [{ label: "Employees", route: "/employees" }],
            },
          ],
        }),
        contacts: moduleSchema({
          name: "contacts",
          navigation: [
            {
              label: "Sales",
              icon: "briefcase",
              order: 1,
              permission: "sales.view",
              children: [{ label: "All Contacts", route: "/", icon: "users", badge_count_route: "/contacts/count" }],
            },
          ],
        }),
      },
    };

    const tree = buildViewRegistry(schema).navigationTree;

    expect(tree.map((g) => g.label)).toEqual(["Sales", "HR"]);
    expect(tree[0]).toMatchObject({
      key: "contacts:sales",
      label: "Sales",
      icon: "briefcase",
      module: "contacts",
      permission: "sales.view",
    });
    expect(tree[0]?.children[0]).toMatchObject({
      key: "contacts:sales:all-contacts",
      label: "All Contacts",
      // route "/" in the contacts module → "/contacts" (no double slash), then /_m prefix.
      path: "/_m/contacts",
      icon: "users",
      badgeCountRoute: "/contacts/count",
    });
    expect(tree[1]?.children[0]?.path).toBe("/_m/hr/employees");
  });

  it("carries a group's and an item's `condition` through to the navigation tree", () => {
    const schema: MetaSchema = {
      engine_version: "test",
      schema_hash: "abc",
      modules: {
        contacts: moduleSchema({
          navigation: [
            {
              label: "Sales",
              order: 1,
              condition: "user_has_role('sales_manager')",
              children: [
                { label: "All", route: "/", condition: "user_has_permission('contacts:contact:read')" },
                { label: "Plain", route: "/plain" },
              ],
            },
          ],
        }),
      },
    };
    const group = buildViewRegistry(schema).navigationTree[0];
    expect(group?.condition).toBe("user_has_role('sales_manager')");
    expect(group?.children[0]?.condition).toBe("user_has_permission('contacts:contact:read')");
    expect(group?.children[1]).not.toHaveProperty("condition");
  });

  it("falls back to default icons when a group/item declares none", () => {
    const schema: MetaSchema = {
      engine_version: "test",
      schema_hash: "abc",
      modules: {
        contacts: moduleSchema({
          navigation: [{ label: "Sales", order: 1, children: [{ label: "All", route: "/" }] }],
        }),
      },
    };
    const tree = buildViewRegistry(schema).navigationTree;
    expect(tree[0]?.icon).toBe("folder");
    expect(tree[0]?.children[0]?.icon).toBe("circle");
  });

  it("skips a group that fails to match the NavGroup schema, without throwing", () => {
    const schema: MetaSchema = {
      engine_version: "test",
      schema_hash: "abc",
      modules: {
        contacts: moduleSchema({
          // biome-ignore lint/suspicious/noExplicitAny: deliberately malformed input for the warn/skip path.
          navigation: [{ label: "Bad" } as any, { label: "Good", order: 1, children: [] }],
        }),
      },
    };
    const tree = buildViewRegistry(schema).navigationTree;
    expect(tree.map((g) => g.label)).toEqual(["Good"]);
  });
});

describe("filterViewByCapability", () => {
  it("clears columns/actions/quick_create on a list-shaped view when the resource has no listPath", () => {
    const view = {
      name: "widgets_list",
      type: "list",
      resource: "widgets.widget",
      label: "Widgets",
      columns: [{ field: "name" }],
      actions: [{ label: "New", type: "create" }],
      quick_create: true,
    };
    filterViewByCapability(view, undefined);
    expect(view.columns).toEqual([]);
    expect(view.actions).toEqual([]);
    expect(view.quick_create).toBe(false);
  });

  it("leaves a view alone when the resource has List", () => {
    const view = {
      name: "widgets_list",
      type: "list",
      resource: "widgets.widget",
      label: "Widgets",
      columns: [{ field: "name" }],
      actions: [
        { label: "New", type: "create" },
        { label: "Export", type: "export" },
      ],
    };
    filterViewByCapability(view, {
      module: "widgets",
      resource: "widgets.widget",
      listPath: "/widgets",
      getPath: "/widgets/{id}",
      createPath: "/widgets",
      updatePath: "/widgets/{id}",
      deletePath: null,
      pivotPath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
    });
    expect(view.columns).toEqual([{ field: "name" }]);
    expect(view.actions).toEqual([
      { label: "New", type: "create" },
      { label: "Export", type: "export" },
    ]);
  });

  it("drops only create-typed actions (and quick_create) when List exists but Create doesn't, across every known action array", () => {
    const view = {
      name: "board",
      type: "kanban",
      resource: "widgets.widget",
      label: "Board",
      actions: [{ label: "New", type: "create" }],
      card_actions: [
        { label: "New card", type: "create" },
        { label: "Archive", type: "route", route: "x.y" },
      ],
      column_actions: [{ label: "New column", type: "create" }],
      quick_create: true,
    };
    filterViewByCapability(view, {
      module: "widgets",
      resource: "widgets.widget",
      listPath: "/widgets",
      getPath: "/widgets/{id}",
      createPath: "",
      updatePath: "/widgets/{id}",
      deletePath: null,
      pivotPath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
    });
    expect(view.actions).toEqual([]);
    expect(view.card_actions).toEqual([{ label: "Archive", type: "route", route: "x.y" }]);
    expect(view.column_actions).toEqual([]);
    expect(view.quick_create).toBe(false);
  });

  it("leaves route/export/import/report/url/custom actions alone regardless of capability — no field expresses their op", () => {
    const view = {
      name: "widgets_list",
      type: "list",
      resource: "widgets.widget",
      label: "Widgets",
      actions: [
        { label: "Export", type: "export" },
        { label: "Run report", type: "report" },
      ],
    };
    filterViewByCapability(view, undefined); // no List at all — everything else about the view is cleared
    // The whole-view-drop path clears the actions array wholesale, but a
    // resource that DOES have List and lacks only Create must leave these untouched.
    const view2 = {
      name: "widgets_list",
      type: "list",
      resource: "widgets.widget",
      label: "Widgets",
      actions: [
        { label: "Export", type: "export" },
        { label: "Run report", type: "report" },
      ],
    };
    filterViewByCapability(view2, {
      module: "widgets",
      resource: "widgets.widget",
      listPath: "/widgets",
      getPath: "/widgets/{id}",
      createPath: "",
      updatePath: "/widgets/{id}",
      deletePath: null,
      pivotPath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
    });
    expect(view.actions).toEqual([]);
    expect(view2.actions).toEqual([
      { label: "Export", type: "export" },
      { label: "Run report", type: "report" },
    ]);
  });
});

describe("buildEmptyViewRegistry", () => {
  it("every accessor answers the same 'nothing loaded' shape a genuinely empty schema would", () => {
    const registry = buildEmptyViewRegistry();
    expect(registry.resolveRoute("/anything")).toBeNull();
    expect(registry.navigationTree).toEqual([]);
    expect(registry.resources.size).toBe(0);
    expect(registry.models.size).toBe(0);
    expect(registry.viewPermissions("anything")).toEqual([]);
    expect(registry.getBundleUrl("anything")).toBeNull();
    expect(registry.getBundleSHA256("anything")).toBeNull();
  });
});
