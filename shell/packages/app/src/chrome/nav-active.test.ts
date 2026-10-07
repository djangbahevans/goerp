import { describe, expect, it } from "vitest";
import { activeNavItemKey, navItemParams } from "./nav-active.js";
import type { NavigationGroup, NavigationItem } from "./navigation-types.js";

const item = (key: string, path: string, extra: Partial<NavigationItem> = {}): NavigationItem => ({
  key,
  label: key,
  path,
  icon: "circle",
  ...extra,
});

const group = (...children: NavigationItem[]): NavigationGroup => ({
  key: "g",
  label: "G",
  icon: "folder",
  module: "contacts",
  children,
});

const LIST = "/_m/contacts/contacts";
const tree = [
  group(
    item("all", LIST),
    item("customers", LIST, { search: { "filter[is_customer]": "true", "filter[is_active]": "true" } }),
    item("suppliers", LIST, { search: { "filter[is_supplier]": "true", "filter[is_active]": "true" } }),
  ),
];

describe("activeNavItemKey", () => {
  it("picks the unfiltered item on the unfiltered list", () => {
    expect(activeNavItemKey(tree, { pathname: LIST, search: {} })).toBe("all");
  });

  it("picks the most specific item whose query the location satisfies", () => {
    const search = { "filter[is_customer]": true, "filter[is_active]": true };
    expect(activeNavItemKey(tree, { pathname: LIST, search })).toBe("customers");
  });

  it("falls back to the broader item when a filter is only partly present", () => {
    expect(activeNavItemKey(tree, { pathname: LIST, search: { "filter[is_customer]": true } })).toBe("all");
  });

  it("matches an item by its default_filters, as the list writes them to the URL", () => {
    const filtered = [group(item("all", LIST), item("customers", LIST, { defaultFilters: { is_customer: true } }))];

    expect(activeNavItemKey(filtered, { pathname: LIST, search: { "filter[is_customer]": true } })).toBe("customers");
    expect(activeNavItemKey(filtered, { pathname: LIST, search: {} })).toBe("all");
  });

  it("keeps an item active on a page beneath its path, preferring the longest path", () => {
    const nested = [group(item("module", "/_m/contacts"), item("list", LIST))];

    expect(activeNavItemKey(nested, { pathname: `${LIST}/01j`, search: {} })).toBe("list");
    expect(activeNavItemKey(nested, { pathname: "/_m/contacts", search: {} })).toBe("module");
  });

  it("does not match a path that only shares a prefix", () => {
    expect(activeNavItemKey([group(item("all", LIST))], { pathname: `${LIST}-archive`, search: {} })).toBeUndefined();
  });

  it("never marks an external item", () => {
    const external = [group(item("docs", "https://docs.example.com", { external: true }))];

    expect(activeNavItemKey(external, { pathname: "/", search: {} })).toBeUndefined();
  });

  it("has no active item off every item's path", () => {
    expect(activeNavItemKey(tree, { pathname: "/activities", search: {} })).toBeUndefined();
  });
});

describe("navItemParams", () => {
  it("combines the route's query with scalar default_filters, ignoring ranges and lists", () => {
    const params = navItemParams(
      item("x", LIST, {
        search: { "filter[a]": "1" },
        defaultFilters: { b: "x", c: 2, d: true, e: ["p"], f: { gte: "1" } },
      }),
    );

    expect(params).toEqual({ "filter[a]": "1", "filter[b]": "x", "filter[c]": "2", "filter[d]": "true" });
  });
});
