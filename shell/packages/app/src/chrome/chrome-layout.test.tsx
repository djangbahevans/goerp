import { localeStore } from "@goerp/sdk/i18n";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChromeLayout } from "./chrome-layout.js";

vi.mock("./chrome-header.js", () => ({
  ChromeHeader: () => (
    <header>
      <button type="button">search</button>
    </header>
  ),
}));
vi.mock("./chrome-sidebar.js", () => ({
  ChromeSidebar: () => (
    <nav aria-label="Main">
      <a href="/sales">Sales</a>
    </nav>
  ),
}));

afterEach(() => {
  cleanup();
  localeStore.setLocale("en");
});

const FOCUSABLE = "a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex='-1'])";

async function renderLayout() {
  const rootRoute = createRootRoute({ component: ChromeLayout });
  const index = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => <p>page content</p>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([index]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  const result = render(<RouterProvider router={router} />);
  return { ...result, router };
}

describe("ChromeLayout", () => {
  it("composes the sidebar, header, and routed content inside <main>", async () => {
    await renderLayout();

    expect(screen.getByRole("navigation", { name: "Main" })).toBeTruthy();
    expect(screen.getByRole("banner")).toBeTruthy();
    const main = screen.getByRole("main");
    expect(main.id).toBe("main-content");
    expect(main.textContent).toContain("page content");
  });

  it("sets dir from the locale direction", async () => {
    const { container } = await renderLayout();
    const root = container.firstElementChild as HTMLElement;
    expect(root.getAttribute("dir")).toBe("ltr");

    act(() => localeStore.setLocale("ar"));
    expect(root.getAttribute("dir")).toBe("rtl");
  });

  it("puts the skip link first in tab order, ahead of the sidebar and header", async () => {
    const { container } = await renderLayout();
    const [first] = container.querySelectorAll(FOCUSABLE);
    expect(first).toBe(screen.getByRole("link", { name: "Skip to content" }));
  });

  it("moves focus to <main> without changing the URL hash", async () => {
    const { router } = await renderLayout();

    fireEvent.click(screen.getByRole("link", { name: "Skip to content" }));

    expect(document.activeElement).toBe(screen.getByRole("main"));
    expect(router.state.location.hash).toBe("");
  });
});
