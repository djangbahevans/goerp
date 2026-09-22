import { describe, expect, it } from "vitest";
import type { MetaSchema, ModuleSchema, ViewExtensionDef, ViewExtensionRef } from "./types.js";
import { buildViewExtensionRegistry } from "./view-extension-registry.js";

function module(
  name: string,
  loadOrder: number,
  viewExtensions: ViewExtensionRef[] = [],
  viewExtensionDefinitions: ViewExtensionDef[] = [],
): ModuleSchema {
  return {
    name,
    version: "1.0.0",
    display_name: name,
    routes: [],
    views: [],
    navigation: [],
    view_extensions: viewExtensions,
    view_extension_definitions: viewExtensionDefinitions,
    load_order: loadOrder,
    models: {},
    permissions: [],
    frontend: null,
    public_config: {},
  };
}

function schema(modules: Record<string, ModuleSchema>): MetaSchema {
  return { engine_version: "test", schema_hash: "abc", modules };
}

const employeesTabDef: ViewExtensionDef = {
  name: "hr_employees_tab",
  type: "tab",
  target_section: "tabs",
  position: "append",
  tab: { label: "Employees", type: "view", view: "hr.employees_list" },
};

describe("buildViewExtensionRegistry", () => {
  it("joins a module's view_extensions against that same module's own view_extension_definitions", () => {
    const registry = buildViewExtensionRegistry(
      schema({
        contacts: module("contacts", 0),
        hr: module("hr", 1, [{ extends: "contacts.contacts_form", extension: "hr_employees_tab" }], [employeesTabDef]),
      }),
    );

    const entries = registry.get("contacts.contacts_form");
    expect(entries).toHaveLength(1);
    expect(entries?.[0]?.module).toBe("hr");
    expect(entries?.[0]?.definition).toBe(employeesTabDef);
  });

  it("leaves definition undefined when `extension` names nothing in the declaring module's own definitions — skipped, not thrown, by callers", () => {
    const registry = buildViewExtensionRegistry(
      schema({
        hr: module("hr", 0, [{ extends: "contacts.contacts_form", extension: "does_not_exist" }], []),
      }),
    );

    const entries = registry.get("contacts.contacts_form");
    expect(entries).toHaveLength(1);
    expect(entries?.[0]?.definition).toBeUndefined();
  });

  it("does not resolve `extension` against a different module's definitions of the same name", () => {
    const sameNameElsewhere: ViewExtensionDef = { ...employeesTabDef, name: "hr_employees_tab" };
    const registry = buildViewExtensionRegistry(
      schema({
        // "payroll" declares a same-named definition, but "hr" is the one
        // referencing it — manifest-spec.md §11: extension resolves within
        // the declaring manifest, not globally.
        payroll: module("payroll", 0, [], [sameNameElsewhere]),
        hr: module("hr", 1, [{ extends: "contacts.contacts_form", extension: "hr_employees_tab" }], []),
      }),
    );

    const entries = registry.get("contacts.contacts_form");
    expect(entries?.[0]?.definition).toBeUndefined();
  });

  it("orders entries targeting the same view by load_order ascending, dependencies first", () => {
    const registry = buildViewExtensionRegistry(
      schema({
        late: module(
          "late",
          5,
          [{ extends: "contacts.contacts_form", extension: "late_tab" }],
          [{ ...employeesTabDef, name: "late_tab" }],
        ),
        early: module(
          "early",
          1,
          [{ extends: "contacts.contacts_form", extension: "early_tab" }],
          [{ ...employeesTabDef, name: "early_tab" }],
        ),
      }),
    );

    const entries = registry.get("contacts.contacts_form");
    expect(entries?.map((e) => e.module)).toEqual(["early", "late"]);
  });

  it("returns no entry for a view nothing extends", () => {
    const registry = buildViewExtensionRegistry(schema({ contacts: module("contacts", 0) }));
    expect(registry.get("contacts.contacts_form")).toBeUndefined();
  });
});
