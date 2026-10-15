import type { ResolvedView, ViewRegistry } from "@goerp/sdk/schema";
import { buildEmptyViewRegistry, ViewRegistryContext, ViewRegistryStatusContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ensureModuleRegistered } from "./module-loader.js";
import { changedRouteResources, useModuleHotReload } from "./use-module-hot-reload.js";

vi.mock("./module-loader.js", () => ({
  ensureModuleRegistered: vi.fn(() => Promise.resolve()),
  registeredBundles: () =>
    new Map([
      ["crm", { bundleUrl: "/bundles/crm-v1.js", bundleSha256: "sha256:/bundles/crm-v1.js" }],
      ["sales", { bundleUrl: "/bundles/sales-v1.js", bundleSha256: "sha256:/bundles/sales-v1.js" }],
    ]),
}));

function view(resource: string, label: string): ResolvedView {
  const module = resource.split(".")[0] as string;
  return {
    module,
    viewName: `${resource}_list`,
    viewType: "list",
    declaration: { name: `${resource}_list`, type: "list", resource, label },
    permissions: [],
    bundleUrl: null,
  };
}

function registry(routes: Record<string, ResolvedView>, bundles: Record<string, string> = {}): ViewRegistry {
  return {
    ...buildEmptyViewRegistry(),
    routes: new Map(Object.entries(routes)),
    getBundleUrl: (module) => bundles[module] ?? null,
    getBundleSHA256: (module) => (bundles[module] ? `sha256:${bundles[module]}` : null),
  };
}

function Probe() {
  useModuleHotReload();
  return null;
}

function renderWith(queryClient: QueryClient, initial: ViewRegistry) {
  const tree = (value: ViewRegistry) => (
    <QueryClientProvider client={queryClient}>
      <ViewRegistryContext.Provider value={value}>
        <ViewRegistryStatusContext.Provider value="ready">
          <Probe />
        </ViewRegistryStatusContext.Provider>
      </ViewRegistryContext.Provider>
    </QueryClientProvider>
  );
  const rendered = render(tree(initial));
  return (next: ViewRegistry) => rendered.rerender(tree(next));
}

afterEach(() => {
  cleanup();
  vi.mocked(ensureModuleRegistered).mockClear();
});

describe("changedRouteResources", () => {
  it("collects resources of changed, added and removed routes only", () => {
    const before = registry({
      "/crm/contacts": view("crm.contact", "Contacts"),
      "/crm/deals": view("crm.deal", "Deals"),
      "/sales/orders": view("sales.order", "Orders"),
    });
    const after = registry({
      "/crm/contacts": view("crm.contact", "People"),
      "/crm/deals": view("crm.deal", "Deals"),
      "/stock/items": view("stock.item", "Items"),
    });
    expect(changedRouteResources(before, after)).toEqual(new Set(["crm.contact", "sales.order", "stock.item"]));
  });
});

describe("useModuleHotReload", () => {
  const routesV1 = { "/crm/contacts": view("crm.contact", "Contacts"), "/crm/deals": view("crm.deal", "Deals") };
  const bundlesV1 = { crm: "/bundles/crm-v1.js", sales: "/bundles/sales-v1.js" };

  it("does nothing for the first registry of the session", () => {
    const queryClient = new QueryClient();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    renderWith(queryClient, registry(routesV1, bundlesV1));
    expect(ensureModuleRegistered).not.toHaveBeenCalled();
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("reloads only the loaded modules whose bundle changed", () => {
    const update = renderWith(new QueryClient(), registry(routesV1, bundlesV1));
    update(registry(routesV1, { ...bundlesV1, crm: "/bundles/crm-v2.js" }));
    expect(vi.mocked(ensureModuleRegistered).mock.calls).toEqual([
      ["crm", "/bundles/crm-v2.js", "sha256:/bundles/crm-v2.js"],
    ]);
  });

  it("invalidates queries of changed routes and keeps the others", () => {
    const queryClient = new QueryClient();
    const contacts = ["infinite-list", null, "crm.contact", {}, null, null];
    const contact = ["record", "crm.contact", "c1"];
    const deals = ["infinite-list", null, "crm.deal", {}, null, null];
    for (const key of [contacts, contact, deals]) queryClient.setQueryData(key, {});
    const update = renderWith(queryClient, registry(routesV1, bundlesV1));

    update(registry({ ...routesV1, "/crm/contacts": view("crm.contact", "People") }, bundlesV1));

    expect(queryClient.getQueryState(contacts)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(contact)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(deals)?.isInvalidated).toBe(false);
  });
});
