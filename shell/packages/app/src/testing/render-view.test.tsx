import { permissionRef } from "@goerp/sdk";
import { defineModule } from "@goerp/sdk/module";
import { useCurrentView } from "@goerp/sdk/nav";
import { resourceRegistry, schemaRegistry } from "@goerp/sdk/schema";
import type { ShellProviderProps } from "@goerp/sdk/testing";
import { cleanup, type RenderResult, screen } from "@testing-library/react";
import { lazy } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../../../modules/demo/frontend/src/api/generated.testing.json";
import { configureViewTests, renderView } from "./index.js";

type Item = Record<string, unknown>;
const renderTestView = renderView as (
  view: string,
  options?: Pick<ShellProviderProps, "apiMocks" | "user" | "permissions"> & {
    api?: object;
    recordId?: string;
    search?: string;
  },
) => Promise<RenderResult>;
const item: Item = {
  id: "item-1",
  name: "Test item",
  tenant_id: "tenant-1",
  created_at: "2026-10-14T00:00:00Z",
  updated_at: "2026-10-14T00:00:00Z",
};
const apiMocks = { "GET /_meta/saved-filters": { data: [] } };

beforeEach(() => {
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  configureViewTests({ fixture, module: defineModule({ name: "demo", api: {} }) });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("renderView", () => {
  it("returns after the list's mocked rows render", async () => {
    const listItems = vi.fn(async () => ({ data: [item], meta: { cursor: null, hasMore: false } }));
    await renderTestView("demo.demo_item_list", { api: { listItems }, apiMocks });
    expect(screen.getByText("Test item")).toBeTruthy();
    expect(listItems).toHaveBeenCalledWith({});
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("hides a create action whose permission is absent", async () => {
    const configured = structuredClone(fixture);
    const list = configured.schema.modules.demo.views.find((view) => view.name === "demo_item_list")!;
    (list as { actions: unknown[] }).actions = [
      { type: "create", label: "Add item", view: "demo_item_form", permission: "demo:item:write" },
    ];
    configureViewTests({ fixture: configured, module: { name: "demo" } });
    const result = await renderTestView("demo.demo_item_list", {
      api: { listItems: async () => ({ data: [item], meta: { cursor: null, hasMore: false } }) },
      apiMocks,
    });
    expect(screen.queryByRole("button", { name: "Add item" })).toBeNull();
    result.unmount();
    await renderTestView("demo.demo_item_list", {
      permissions: [permissionRef("demo:item:write")],
      api: { listItems: async () => ({ data: [item], meta: { cursor: null, hasMore: false } }) },
      apiMocks,
    });
    expect(screen.getByRole("button", { name: "Add item" })).toBeTruthy();
  });

  it("opens the selected record and an empty create form", async () => {
    const getItem = vi.fn(async () => item);
    const result = await renderTestView("demo.demo_item_form", { recordId: String(item.id), api: { getItem } });
    expect(getItem).toHaveBeenCalledWith(item.id);
    expect(screen.getByText("Test item")).toBeTruthy();
    result.unmount();
    await renderTestView("demo.demo_item_form", { api: { getItem } });
    expect(screen.getByRole("textbox", { name: /Name/ })).toHaveProperty("value", "");
    expect(getItem).toHaveBeenCalledTimes(1);
  });

  it("mounts custom components with the view context and router search", async () => {
    const configured = structuredClone(fixture);
    configured.schema.modules.demo.views.push({
      name: "custom",
      type: "custom",
      resource: "demo.item",
      label: "Custom",
      component: "TestingCustom",
    } as never);
    const TestingCustom = lazy(async () => ({
      default: () => {
        const view = useCurrentView();
        return <p>Custom {view?.params.get("stage")}</p>;
      },
    }));
    configureViewTests({ fixture: configured, module: defineModule({ name: "demo", views: { TestingCustom } }) });
    await renderTestView("demo.custom", { search: "stage=lead" });
    expect(screen.getByText("Custom lead")).toBeTruthy();
  });

  it("mounts a defineModule field renderer in a create form", async () => {
    const configured = structuredClone(fixture);
    const form = configured.schema.modules.demo.views.find((view) => view.name === "demo_item_form")!;
    (form as { sections: unknown[] }).sections = [
      { fields: [{ field: "name", label: "Name", type: "custom", component: "demo:testing-name" }] },
    ];
    const NameRenderer = lazy(async () => ({ default: () => <span>Custom name field</span> }));
    configureViewTests({
      fixture: configured,
      module: defineModule({ name: "demo", fieldRenderers: { "demo:testing-name": NameRenderer } }),
    });
    await renderTestView("demo.demo_item_form");
    expect(screen.getByText("Custom name field")).toBeTruthy();
  });

  it("reports the unmocked method, path and generated function", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    await renderTestView("demo.demo_item_list", { apiMocks });
    expect(screen.getByText(/Unmocked API request: GET .*demo\/items \(demo.listItems\)/)).toBeTruthy();
    expect(error).toHaveBeenCalledWith(expect.stringContaining("demo.listItems"));
  });

  it("restores schema and registry methods on unmount", async () => {
    const originalSchema = schemaRegistry.getSchema;
    const originalResolve = resourceRegistry.resolve;
    const result = await renderTestView("demo.demo_item_list", {
      api: { listItems: async () => ({ data: [item], meta: { cursor: null, hasMore: false } }) },
      apiMocks,
    });
    expect(schemaRegistry.getSchema).not.toBe(originalSchema);
    result.unmount();
    expect(schemaRegistry.getSchema).toBe(originalSchema);
    expect(resourceRegistry.resolve).toBe(originalResolve);
  });

  it("uses raw route mocks before generated mocks", async () => {
    const listItems = vi.fn();
    await renderTestView("demo.demo_item_list", {
      api: { listItems },
      apiMocks: { ...apiMocks, "GET /demo/items": { data: [item], meta: { cursor: null, has_more: false } } },
    });
    expect(screen.getByText("Test item")).toBeTruthy();
    expect(listItems).not.toHaveBeenCalled();
  });
});

describe("renderView navigation", () => {
  it("dispatches the create form when the list's create action navigates", async () => {
    await renderTestView("demo.demo_item_list", {
      api: { listItems: async () => ({ data: [item], meta: { cursor: null, hasMore: false } }) },
      apiMocks,
    });
    const { fireEvent } = await import("@testing-library/react");
    fireEvent.click(screen.getByRole("button", { name: "New Item" }));
    expect(await screen.findByRole("textbox", { name: /Name/ })).toHaveProperty("value", "");
  });
});
