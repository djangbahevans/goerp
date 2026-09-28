import { useUnreadCount } from "@goerp/sdk/notifications";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDueActivityCount } from "../activities/use-due-activity-count.js";
import { BottomNavBar } from "./bottom-nav-bar.js";
import { openCommandPalette } from "./command-palette-control.js";
import type { NavigationGroup } from "./navigation-types.js";

const TREE: NavigationGroup[] = [
  {
    key: "sales",
    label: "Sales",
    icon: "shopping-cart",
    module: "sales",
    children: [{ key: "orders", label: "Orders", icon: "file-text", path: "/sales/orders" }],
  },
];

vi.mock("@goerp/sdk/notifications", () => ({ useUnreadCount: vi.fn() }));
vi.mock("../activities/use-due-activity-count.js", () => ({ useDueActivityCount: vi.fn() }));
vi.mock("./command-palette-control.js", () => ({ openCommandPalette: vi.fn() }));
vi.mock("./use-navigation-tree.js", () => ({ useNavigationTree: () => TREE }));
vi.mock("./sidebar-store.js", () => ({
  useSidebar: () => ({
    collapsed: false,
    expandedGroups: new Set(["sales"]),
    toggleCollapsed: vi.fn(),
    toggleGroup: vi.fn(),
  }),
}));
vi.mock("./notification-sheet.js", () => ({
  NotificationSheet: ({ open }: { open: boolean }) => <div data-testid="notification-sheet" data-open={open} />,
}));

beforeEach(() => {
  vi.mocked(useUnreadCount).mockReturnValue({ count: 0 });
  vi.mocked(useDueActivityCount).mockReturnValue(0);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

async function renderBar(path = "/") {
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <BottomNavBar />
      </>
    ),
  });
  const routes = ["/", "/activities", "/sales/orders"].map((routePath) =>
    createRoute({ getParentRoute: () => rootRoute, path: routePath, component: () => <p>{routePath}</p> }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return router;
}

function bar(): HTMLElement {
  return screen.getByRole("navigation", { name: "Main" });
}

describe("BottomNavBar", () => {
  it("renders the five items in a Main landmark, with Home current at /", async () => {
    await renderBar();

    const nav = within(bar());
    expect(nav.getByRole("link", { name: "Home" }).getAttribute("aria-current")).toBe("page");
    expect(nav.getByRole("button", { name: "Search" })).toBeTruthy();
    expect(nav.getByRole("link", { name: "Activities" }).getAttribute("aria-current")).toBeNull();
    expect(nav.getByRole("button", { name: "Notifications" })).toBeTruthy();
    expect(nav.getByRole("button", { name: "More" })).toBeTruthy();
  });

  it("marks Activities current on /activities", async () => {
    await renderBar("/activities");

    const nav = within(bar());
    expect(nav.getByRole("link", { name: "Activities" }).getAttribute("aria-current")).toBe("page");
    expect(nav.getByRole("link", { name: "Home" }).getAttribute("aria-current")).toBeNull();
  });

  it("puts the due and unread counts in the item names, capped at 99+", async () => {
    vi.mocked(useDueActivityCount).mockReturnValue(3);
    vi.mocked(useUnreadCount).mockReturnValue({ count: 140 });
    await renderBar();

    const nav = within(bar());
    expect(nav.getByRole("link", { name: "Activities, 3 due" })).toBeTruthy();
    expect(nav.getByRole("button", { name: "Notifications, 99+ unread" })).toBeTruthy();
  });

  it("opens the command palette from Search and the notification sheet from Notifications", async () => {
    await renderBar();

    fireEvent.click(within(bar()).getByRole("button", { name: "Search" }));
    expect(openCommandPalette).toHaveBeenCalledOnce();

    const notifications = within(bar()).getByRole("button", { name: "Notifications" });
    fireEvent.click(notifications);
    expect(screen.getByTestId("notification-sheet").dataset.open).toBe("true");
    expect(notifications.getAttribute("aria-expanded")).toBe("true");
  });

  it("opens a modal Menu sheet with the nav tree, focusing its heading", async () => {
    await renderBar();

    fireEvent.click(within(bar()).getByRole("button", { name: "More" }));

    const sheet = await screen.findByRole("dialog", { name: "Menu" });
    await waitFor(() => expect(document.activeElement).toBe(within(sheet).getByRole("heading", { name: "Menu" })));
    expect(within(sheet).getByRole("button", { name: "Sales" }).getAttribute("aria-expanded")).toBe("true");
    expect(within(sheet).getByRole("link", { name: "Orders" })).toBeTruthy();
  });

  it("closes the sheet when a destination is chosen, navigates there, and leaves focus off More", async () => {
    const router = await renderBar();

    const more = within(bar()).getByRole("button", { name: "More" });
    act(() => more.focus());
    fireEvent.click(more);
    const sheet = await screen.findByRole("dialog", { name: "Menu" });
    fireEvent.click(within(sheet).getByRole("link", { name: "Orders" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Menu" })).toBeNull());
    await waitFor(() => expect(router.state.location.pathname).toBe("/sales/orders"));
    expect(document.activeElement).not.toBe(more);
  });

  it("closes on Escape and returns focus to More", async () => {
    await renderBar();

    const more = within(bar()).getByRole("button", { name: "More" });
    act(() => more.focus());
    fireEvent.click(more);
    const sheet = await screen.findByRole("dialog", { name: "Menu" });
    fireEvent.keyDown(sheet, { key: "Escape" });

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Menu" })).toBeNull());
    expect(document.activeElement).toBe(more);
  });
});
