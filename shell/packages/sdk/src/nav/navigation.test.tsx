import { act, cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import { resourceRef } from "../names.js";
import { ModuleNavigationProvider, type NavigateFn } from "../react/use-module.js";
import type { MetaSchema } from "../schema/types.js";
import { buildEmptyViewRegistry, buildViewRegistry } from "../schema/view-registry.js";
import { ViewRegistryContext } from "../schema/view-registry-provider.js";
import { CurrentViewProvider, useCurrentView, useModuleLink, useNavigate, useResourceRegistry } from "./index.js";

function schema(recordPath = "/contacts/records/{id}"): MetaSchema {
  const route = (path: string, crud_action: string, view: string, method = "GET") => ({
    path,
    crud_action,
    view,
    method,
    model: "contacts.contact",
    permissions: [],
    response_is_list: false,
    engine_native: true,
  });
  return {
    engine_version: "test",
    schema_hash: recordPath,
    modules: {
      contacts: {
        name: "contacts",
        version: "0.1.0",
        display_name: "Contacts",
        permissions: [],
        frontend: null,
        view_extensions: [],
        view_extension_definitions: [],
        error_codes: [],
        notification_types: [],
        load_order: 0,
        public_config: {},
        navigation: [],
        models: {
          "contacts.contact": {
            name: "contact",
            label: "Contact",
            label_plural: "Contacts",
            fields: [{ name: "name", type: "string", label: "Name" }],
            enabled_ops: ["list", "get", "create"],
            shareable: false,
          },
        },
        routes: [
          route("/contacts", "list", "contacts_list"),
          route("/contacts", "create", "contacts_form", "POST"),
          route(recordPath, "get", "contacts_form"),
        ],
        views: [
          { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" },
          { name: "contacts_form", type: "form", resource: "contacts.contact", label: "Contact", sections: [] },
        ],
      },
    },
  };
}

afterEach(cleanup);

describe("navigation hooks", () => {
  it("adds the module prefix, preserves queries and forwards replace and shell options", () => {
    const navigate = vi.fn<NavigateFn>();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ModuleNavigationProvider navigate={navigate}>{children}</ModuleNavigationProvider>
    );
    const { result } = renderHook(useNavigate, { wrapper });
    result.current("/contacts/c1?tab=notes");
    result.current("/contacts/new", { replace: true });
    result.current("/settings/profile", { shell: true, replace: true });
    expect(navigate.mock.calls).toEqual([
      ["/_m/contacts/c1?tab=notes", undefined],
      ["/_m/contacts/new", { replace: true }],
      ["/settings/profile", { shell: true, replace: true }],
    ]);
  });

  it("requires a navigation provider", () => {
    expect(() => renderHook(useNavigate)).toThrow(/ModuleNavigationProvider/);
  });

  it("returns null outside a view and the nearest view inside nested providers", () => {
    expect(renderHook(useCurrentView).result.current).toBeNull();
    const parent = {
      viewName: "contacts_form",
      moduleName: "contacts",
      resource: "contacts.contact",
      recordId: "c1",
      embedded: false,
      params: new URLSearchParams("tab=notes"),
    };
    const embedded = { ...parent, viewName: "notes_list", embedded: true, params: new URLSearchParams() };
    const wrapper = ({ children }: { children: ReactNode }) => (
      <CurrentViewProvider value={parent}>
        <CurrentViewProvider value={embedded}>{children}</CurrentViewProvider>
      </CurrentViewProvider>
    );
    expect(renderHook(useCurrentView, { wrapper }).result.current).toBe(embedded);
  });
});

describe("resource navigation", () => {
  it("reads the resource metadata and links to the default form's record route", () => {
    const navigate = vi.fn<NavigateFn>();
    const registry = buildViewRegistry(schema());
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ModuleNavigationProvider navigate={navigate}>
        <PermissionContext
          value={createPermissionContextValue({
            permissions: new Set(),
            fieldAccess: {},
            modulesEnabled: new Set(["contacts"]),
          })}
        >
          <ViewRegistryContext value={registry}>{children}</ViewRegistryContext>
        </PermissionContext>
      </ModuleNavigationProvider>
    );
    const { result } = renderHook(
      () => ({
        registry: useResourceRegistry(),
        link: useModuleLink(resourceRef("contacts.contact"), "id/with?#spaces "),
      }),
      { wrapper },
    );
    expect(result.current.registry.get("contacts.contact")).toMatchObject({
      module: "contacts",
      defaultFormView: "contacts_form",
      labelField: "name",
    });
    expect(result.current.registry.get("unknown")).toBeUndefined();
    expect(result.current.link.href).toBe("/_m/contacts/records/id%2Fwith%3F%23spaces%20");
    expect(result.current.link.isAvailable).toBe(true);
    result.current.link.navigate();
    expect(navigate).toHaveBeenCalledWith("/_m/contacts/records/id%2Fwith%3F%23spaces%20", undefined);
  });

  it("updates metadata and links as the snapshot and module enablement change", () => {
    const navigate = vi.fn<NavigateFn>();
    let registry = buildEmptyViewRegistry();
    let enabled = true;
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ModuleNavigationProvider navigate={navigate}>
        <PermissionContext
          value={createPermissionContextValue({
            permissions: new Set(),
            fieldAccess: {},
            modulesEnabled: new Set(enabled ? ["contacts"] : []),
          })}
        >
          <ViewRegistryContext value={registry}>{children}</ViewRegistryContext>
        </PermissionContext>
      </ModuleNavigationProvider>
    );
    const { result, rerender } = renderHook(
      () => ({ registry: useResourceRegistry(), link: useModuleLink("contacts.contact", "c1") }),
      { wrapper },
    );
    expect(result.current.registry.get("contacts.contact")).toBeUndefined();
    expect(result.current.link.href).toBeNull();
    result.current.link.navigate();
    expect(navigate).not.toHaveBeenCalled();
    registry = buildViewRegistry(schema());
    rerender();
    expect(result.current.link.href).toBe("/_m/contacts/records/c1");
    registry = buildViewRegistry(schema("/contacts/people/{id}"));
    rerender();
    expect(result.current.link.href).toBe("/_m/contacts/people/c1");
    enabled = false;
    rerender();
    expect(result.current.link.isAvailable).toBe(false);
    enabled = true;
    registry = buildEmptyViewRegistry();
    rerender();
    expect(result.current.link.href).toBeNull();
  });

  it.each(["no form", "no record route"])("is unavailable with %s", (missing) => {
    const source = schema();
    const contacts = source.modules.contacts;
    if (!contacts) throw new Error("Missing fixture module");
    if (missing === "no form") contacts.views = contacts.views.slice(0, 1);
    else contacts.routes = contacts.routes.slice(0, 2);
    const navigate = vi.fn<NavigateFn>();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ModuleNavigationProvider navigate={navigate}>
        <PermissionContext
          value={createPermissionContextValue({
            permissions: new Set(),
            fieldAccess: {},
            modulesEnabled: new Set(["contacts"]),
          })}
        >
          <ViewRegistryContext value={buildViewRegistry(source)}>{children}</ViewRegistryContext>
        </PermissionContext>
      </ModuleNavigationProvider>
    );
    const { result } = renderHook(() => useModuleLink("contacts.contact", "c1"), { wrapper });
    expect(result.current.isAvailable).toBe(false);
    expect(result.current.href).toBeNull();
    act(() => result.current.navigate());
    expect(navigate).not.toHaveBeenCalled();
  });
});
