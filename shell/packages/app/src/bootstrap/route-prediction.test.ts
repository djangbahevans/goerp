import type { NavigationGroup, ResolvedView } from "@goerp/sdk/schema";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type PredictionInput,
  predictPreloadModules,
  readModuleHistory,
  recordModuleVisit,
} from "./route-prediction.js";

function view(module: string, overrides: Partial<ResolvedView> = {}): ResolvedView {
  return {
    module,
    viewName: `${module}_list`,
    viewType: "list",
    declaration: { name: `${module}_list`, type: "list", resource: `${module}.record`, label: module },
    permissions: [],
    bundleUrl: `/bundles/${module}.js`,
    ...overrides,
  };
}

function group(module: string, paths: string[]): NavigationGroup {
  return {
    key: module,
    label: module,
    icon: "folder",
    module,
    children: paths.map((path) => ({ key: path, label: path, path, icon: "circle" })),
  };
}

function input(
  views: Record<string, ResolvedView>,
  navigationTree: NavigationGroup[],
  overrides: Partial<PredictionInput> = {},
): PredictionInput {
  return {
    registry: { navigationTree, resolveRoute: (path) => views[path] ?? null },
    permissions: { check: () => true, moduleEnabled: () => true },
    history: [],
    ...overrides,
  };
}

const VIEWS = {
  "/crm/contacts": view("crm"),
  "/crm/deals": view("crm"),
  "/sales/orders": view("sales"),
  "/stock/items": view("stock"),
  "/hr/staff": view("hr"),
};
const TREE = [
  group("crm", ["/crm/contacts", "/crm/deals"]),
  group("sales", ["/sales/orders"]),
  group("stock", ["/stock/items"]),
  group("hr", ["/hr/staff"]),
];

describe("predictPreloadModules", () => {
  it("takes the first three distinct modules in navigation order without history", () => {
    expect(predictPreloadModules(input(VIEWS, TREE))).toEqual(["crm", "sales", "stock"]);
  });

  it("ranks recently visited modules first, most recent first", () => {
    expect(predictPreloadModules(input(VIEWS, TREE, { history: ["hr", "stock"] }))).toEqual(["hr", "stock", "crm"]);
  });

  it("skips the current module", () => {
    expect(predictPreloadModules(input(VIEWS, TREE, { history: ["hr"], exclude: "hr" }))).toEqual([
      "crm",
      "sales",
      "stock",
    ]);
  });

  it("skips routes the user can't open and modules without a bundle", () => {
    const views = {
      "/crm/contacts": view("crm"),
      "/sales/orders": view("sales", { permissions: ["sales:order:read"] }),
      "/stock/items": view("stock", { bundleUrl: null }),
      "/hr/staff": view("hr"),
      "/billing/invoices": view("billing"),
    };
    const hr = group("hr", ["/hr/staff"]);
    const tree = [
      group("crm", ["/crm/contacts"]),
      group("sales", ["/sales/orders"]),
      group("stock", ["/stock/items"]),
      { ...hr, children: hr.children.map((item) => ({ ...item, permission: "hr:staff:read" })) },
      group("billing", ["/billing/invoices"]),
    ];
    const permissions = { check: () => false, moduleEnabled: (module: string) => module !== "crm" };
    expect(predictPreloadModules(input(views, tree, { permissions }))).toEqual(["billing"]);
  });

  it("skips external links", () => {
    const tree = [{ ...group("docs", ["/crm/contacts"]), children: [{ ...TREE[0]!.children[0]!, external: true }] }];
    expect(predictPreloadModules(input(VIEWS, tree))).toEqual([]);
  });
});

function memoryStorage(): Pick<Storage, "getItem" | "setItem"> {
  const items = new Map<string, string>();
  return {
    getItem: (key) => items.get(key) ?? null,
    setItem: (key, value) => void items.set(key, value),
  };
}

describe("module history", () => {
  let storage: Pick<Storage, "getItem" | "setItem">;
  beforeEach(() => {
    storage = memoryStorage();
    vi.stubGlobal("window", { localStorage: storage });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps each user's visits most recent first without duplicates", () => {
    recordModuleVisit("u1", "crm");
    recordModuleVisit("u1", "sales");
    recordModuleVisit("u1", "crm");
    recordModuleVisit("u2", "hr");
    expect(readModuleHistory("u1")).toEqual(["crm", "sales"]);
    expect(readModuleHistory("u2")).toEqual(["hr"]);
  });

  it("keeps the ten most recent modules", () => {
    for (let i = 0; i < 12; i++) recordModuleVisit("u1", `m${i}`);
    expect(readModuleHistory("u1")).toEqual(["m11", "m10", "m9", "m8", "m7", "m6", "m5", "m4", "m3", "m2"]);
  });

  it("treats unreadable or unwritable storage as empty history", () => {
    storage.setItem("goerp-module-history:u1", "{not json");
    expect(readModuleHistory("u1")).toEqual([]);

    storage.setItem = () => {
      throw new Error("QuotaExceededError");
    };
    expect(() => recordModuleVisit("u1", "crm")).not.toThrow();
  });
});
