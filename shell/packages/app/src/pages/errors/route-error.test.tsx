import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { rendersErrorPage } from "./chromeless.js";
import { RouteError } from "./route-error.js";

function setBrowserOnline(online: boolean) {
  act(() => {
    window.dispatchEvent(new Event(online ? "online" : "offline"));
  });
}

afterEach(() => {
  setBrowserOnline(true);
  cleanup();
});

async function renderInRouter(node: ReactNode) {
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute({
    component: () => <PermissionContext.Provider value={permissions}>{node}</PermissionContext.Provider>,
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

const serverError = new AppError({ httpStatus: 503, code: "unavailable", message: "Service unavailable" });

describe("RouteError", () => {
  it("renders the offline message with Try again while offline, whatever the failure", async () => {
    setBrowserOnline(false);
    const reset = vi.fn();
    await renderInRouter(<RouteError error={serverError} reset={reset} />);

    expect(screen.getByText("This page isn't available offline.")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Something went wrong on our end" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(reset).toHaveBeenCalledTimes(1);
  });

  it("renders the 500 page for the same failure while online", async () => {
    await renderInRouter(<RouteError error={serverError} reset={vi.fn()} />);
    expect(screen.getByRole("heading", { name: "Something went wrong on our end" })).toBeTruthy();
    expect(screen.queryByText("This page isn't available offline.")).toBeNull();
  });
});

describe("rendersErrorPage", () => {
  const failed = [{ status: "error", error: serverError }];

  it("drops the chrome for a server failure only while online", () => {
    expect(rendersErrorPage(failed)).toBe(true);
    expect(rendersErrorPage(failed, false)).toBe(false);
  });

  it("still drops the chrome for a not-found match while offline", () => {
    expect(rendersErrorPage([{ status: "notFound", error: undefined }], false)).toBe(true);
  });
});
