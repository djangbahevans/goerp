import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChromeBanner } from "./chrome-banner.js";

afterEach(cleanup);

async function renderInRouter(node: ReactNode) {
  const rootRoute = createRootRoute({ component: () => node });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("ChromeBanner", () => {
  it("is a polite status region with a decorative icon", async () => {
    await renderInRouter(<ChromeBanner tone="warning">Heads up.</ChromeBanner>);

    const banner = screen.getByRole("status");
    expect(banner.textContent).toContain("Heads up.");
    expect(banner.className).toContain("bg-warning-subtle");
    expect(banner.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("uses the info tint for the info tone", async () => {
    await renderInRouter(<ChromeBanner tone="info">FYI.</ChromeBanner>);
    expect(screen.getByRole("status").className).toContain("bg-info-subtle");
  });

  it("renders the action as an in-app link, including its hash", async () => {
    await renderInRouter(
      <ChromeBanner
        tone="warning"
        action={{ label: "Update password", to: "/settings/profile", hash: "change-password" }}
      >
        Update it.
      </ChromeBanner>,
    );

    expect(screen.getByRole("link", { name: "Update password" }).getAttribute("href")).toBe(
      "/settings/profile#change-password",
    );
  });

  it("has no dismiss button without onDismiss", async () => {
    await renderInRouter(<ChromeBanner tone="warning">Offline.</ChromeBanner>);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("calls onDismiss from a button named by dismissLabel", async () => {
    const onDismiss = vi.fn();
    await renderInRouter(
      <ChromeBanner tone="warning" onDismiss={onDismiss} dismissLabel="Dismiss password notice">
        Update it.
      </ChromeBanner>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Dismiss password notice" }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
