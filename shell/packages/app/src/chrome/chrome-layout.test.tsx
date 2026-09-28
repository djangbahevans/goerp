import { passwordUpdateNotice } from "@goerp/sdk/auth";
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
vi.mock("./bottom-nav-bar.js", () => ({
  BottomNavBar: () => <nav aria-label="Main">bottom bar</nav>,
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
  vi.unstubAllGlobals();
  localeStore.setLocale("en");
  passwordUpdateNotice.set(false);
});

const FOCUSABLE = "a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex='-1'])";

async function renderLayout() {
  const rootRoute = createRootRoute({ component: ChromeLayout });
  const index = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => <p>page content</p>,
  });
  const other = createRoute({
    getParentRoute: () => rootRoute,
    path: "/other",
    component: () => <p>other page</p>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([index, other]),
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

  it("places session banners between the header and <main>", async () => {
    passwordUpdateNotice.set(true);
    await renderLayout();

    const header = screen.getByRole("banner");
    const status = screen.getByRole("status");
    const main = screen.getByRole("main");
    expect(header.compareDocumentPosition(status) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(status.compareDocumentPosition(main) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(status.textContent).toContain("updated its password requirements");
  });

  it("moves focus to <main> when a banner is dismissed", async () => {
    passwordUpdateNotice.set(true);
    await renderLayout();

    fireEvent.click(screen.getByRole("button", { name: "Dismiss password notice" }));

    expect(screen.queryByRole("status")).toBeNull();
    expect(passwordUpdateNotice.get()).toBe(false);
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("leaves focus alone on the initial render", async () => {
    await renderLayout();
    expect(document.activeElement).not.toBe(screen.getByRole("main"));
  });

  it("moves focus to <main> when the pathname changes", async () => {
    const { router } = await renderLayout();
    screen.getByRole("button", { name: "search" }).focus();

    await act(() => router.navigate({ href: "/other" }));

    expect(screen.getByRole("main").textContent).toContain("other page");
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("keeps focus in place when only the search params or hash change", async () => {
    const { router } = await renderLayout();
    const search = screen.getByRole("button", { name: "search" });
    search.focus();

    await act(() => router.navigate({ href: "/?sort=name" }));
    await act(() => router.navigate({ href: "/?sort=name#lines" }));

    expect(document.activeElement).toBe(search);
  });

  it("renders no banner when nothing is active", async () => {
    await renderLayout();
    expect(screen.queryByRole("status")).toBeNull();
  });
});

// A matchMedia whose (min-width: 768px) answer can change mid-test.
function stubViewport(initiallyWide: boolean) {
  let wide = initiallyWide;
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    get matches() {
      return wide;
    },
    media: query,
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
  }));
  return {
    resize(nextWide: boolean) {
      wide = nextWide;
      for (const listener of listeners) listener();
    },
  };
}

describe("ChromeLayout below 768px", () => {
  it("renders the bottom bar instead of the rail, padding the content column by its height", async () => {
    stubViewport(false);
    await renderLayout();

    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(nav.textContent).toBe("bottom bar");
    expect(screen.queryByRole("link", { name: "Sales" })).toBeNull();
    expect(screen.getByRole("main").parentElement?.className).toContain("pb-[calc(var(--bottom-nav-height)");
  });

  it("swaps back to the rail when the viewport widens to 768px", async () => {
    const viewport = stubViewport(false);
    await renderLayout();

    act(() => viewport.resize(true));

    expect(screen.getByRole("navigation", { name: "Main" }).textContent).toBe("Sales");
    expect(screen.queryByText("bottom bar")).toBeNull();
    expect(screen.getByRole("main").parentElement?.className).not.toContain("--bottom-nav-height");
  });
});
