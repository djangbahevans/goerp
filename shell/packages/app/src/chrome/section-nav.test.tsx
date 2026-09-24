import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mostSpecificItem, SectionNav, type SectionNavGroup, type SectionNavLayout } from "./section-nav.js";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const GROUPS: SectionNavGroup[] = [
  {
    heading: "Access",
    items: [
      { to: "/admin/users", label: "Users", icon: "users" },
      { to: "/admin/roles", label: "Roles" },
    ],
  },
  { heading: "Empty", items: [] },
  { heading: "Platform", items: [{ to: "/admin/modules", label: "Modules" }] },
];

const NESTED_GROUPS: SectionNavGroup[] = [
  {
    items: [
      { to: "/admin/settings", label: "Tenant settings" },
      { to: "/admin/settings/notifications", label: "Notification templates" },
    ],
  },
];

async function renderNav(path: string, layout?: SectionNavLayout, groups: SectionNavGroup[] = GROUPS) {
  const rootRoute = createRootRoute({
    component: () => (
      <SectionNav label="Administration" groups={groups} layout={layout}>
        <Outlet />
      </SectionNav>
    ),
  });
  const page = (routePath: string) =>
    createRoute({ getParentRoute: () => rootRoute, path: routePath, component: () => <p>page {routePath}</p> });
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      page("/admin/users"),
      page("/admin/users/$id"),
      page("/admin/roles"),
      page("/admin/modules"),
      page("/admin/settings"),
      page("/admin/settings/notifications"),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return screen.getByRole("navigation", { name: "Administration" });
}

function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches, addEventListener: vi.fn(), removeEventListener: vi.fn() })),
  );
}

describe("SectionNav", () => {
  it("renders the landmark next to the routed content", async () => {
    await renderNav("/admin/roles", "wide");
    expect(screen.getByText("page /admin/roles")).toBeTruthy();
  });

  it("marks the current page's item with aria-current", async () => {
    const nav = await renderNav("/admin/roles", "wide");
    expect(within(nav).getByRole("link", { name: "Roles" }).getAttribute("aria-current")).toBe("page");
    expect(within(nav).getByRole("link", { name: "Users" }).getAttribute("aria-current")).toBeNull();
  });

  it("keeps an item active on its nested pages", async () => {
    const nav = await renderNav("/admin/users/01j", "wide");
    expect(within(nav).getByRole("link", { name: "Users" }).getAttribute("aria-current")).toBe("page");
  });

  it("marks only the most specific item when one item's path is a parent of another's", async () => {
    const nav = await renderNav("/admin/settings/notifications", "wide", NESTED_GROUPS);
    const current = within(nav)
      .getAllByRole("link")
      .filter((link) => link.getAttribute("aria-current") === "page");
    expect(current.map((link) => link.textContent)).toEqual(["Notification templates"]);
  });

  it("keeps the parent item active on its own page", async () => {
    const nav = await renderNav("/admin/settings", "wide", NESTED_GROUPS);
    expect(within(nav).getByRole("link", { name: "Tenant settings" }).getAttribute("aria-current")).toBe("page");
    expect(within(nav).getByRole("link", { name: "Notification templates" }).getAttribute("aria-current")).toBeNull();
  });

  it("labels each headed group's list and drops empty groups with their heading", async () => {
    const nav = await renderNav("/admin/users", "wide");
    expect(within(nav).getByRole("list", { name: "Access" })).toBeTruthy();
    expect(within(nav).getByRole("list", { name: "Platform" })).toBeTruthy();
    expect(within(nav).queryByText("Empty")).toBeNull();
  });

  it("renders every item in one unlabelled list in the narrow layout", async () => {
    const nav = await renderNav("/admin/users", "narrow");
    const lists = within(nav).getAllByRole("list");
    expect(lists).toHaveLength(1);
    expect(within(lists[0] as HTMLElement).getAllByRole("link")).toHaveLength(3);
    expect(within(nav).queryByText("Access")).toBeNull();
  });

  it("scrolls the active item into view in the narrow layout", async () => {
    const scrollIntoView = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scrollIntoView;
    try {
      await renderNav("/admin/modules", "narrow");
    } finally {
      Element.prototype.scrollIntoView = original;
    }
    expect(scrollIntoView).toHaveBeenCalledTimes(1);
    expect(scrollIntoView.mock.contexts[0]).toBe(screen.getByRole("link", { name: "Modules" }));
  });

  it("follows the viewport when no layout is forced", async () => {
    stubMatchMedia(false);
    const narrow = await renderNav("/admin/users");
    expect(within(narrow).getAllByRole("list")).toHaveLength(1);
    cleanup();

    stubMatchMedia(true);
    const wide = await renderNav("/admin/users");
    expect(within(wide).getAllByRole("list")).toHaveLength(2);
  });
});

describe("mostSpecificItem", () => {
  it("picks the longest item path that equals or contains the pathname", () => {
    expect(mostSpecificItem("/admin/settings/notifications", NESTED_GROUPS)).toBe("/admin/settings/notifications");
    expect(mostSpecificItem("/admin/settings/", NESTED_GROUPS)).toBe("/admin/settings");
    expect(mostSpecificItem("/admin/settings-archive", NESTED_GROUPS)).toBeNull();
  });
});
