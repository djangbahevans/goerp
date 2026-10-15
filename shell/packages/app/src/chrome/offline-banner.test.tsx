import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChromeBanners } from "./chrome-banners.js";

vi.mock("./password-update-banner.js", () => ({ PasswordUpdateBanner: () => <p>Password notice</p> }));

function setBrowserOnline(online: boolean) {
  act(() => {
    window.dispatchEvent(new Event(online ? "online" : "offline"));
  });
}

afterEach(() => {
  setBrowserOnline(true);
  cleanup();
});

async function renderBanners() {
  const rootRoute = createRootRoute({ component: () => <ChromeBanners mainRef={{ current: null }} /> });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("OfflineBanner", () => {
  it("shows a warning without a dismiss button while offline and clears when back online", async () => {
    await renderBanners();
    expect(screen.queryByRole("status")).toBeNull();

    setBrowserOnline(false);
    const banner = screen.getByRole("status");
    expect(banner.textContent).toBe("You're offline. Changes can't be saved until you reconnect.");
    expect(banner.className).toContain("bg-warning-subtle");
    expect(screen.queryByRole("button")).toBeNull();

    setBrowserOnline(true);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("comes before the account notices", async () => {
    await renderBanners();
    setBrowserOnline(false);
    const banner = screen.getByRole("status");
    const notice = screen.getByText("Password notice");
    expect(banner.compareDocumentPosition(notice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
