import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ListActions } from "./list-actions.js";
import type { ListAction } from "./list-view-types.js";

const { useActionMock, resolveViewPathMock } = vi.hoisted(() => ({
  useActionMock: vi.fn(),
  resolveViewPathMock: vi.fn(async (): Promise<string | null> => "/contacts/new"),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useAction: useActionMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewPathRegistry: { resolve: resolveViewPathMock } };
});

beforeEach(() => {
  useActionMock.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false, error: null });
});

afterEach(() => {
  cleanup();
  useActionMock.mockReset();
  resolveViewPathMock.mockClear();
});

function permissionWrapper(permissions: string[]) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

async function renderActions(actions: ListAction[], Wrapper: ({ children }: { children: ReactNode }) => ReactNode) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <Wrapper>
        <ListActions actions={actions} module="contacts" />
      </Wrapper>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return router;
}

const fullAccess = permissionWrapper(["contacts:contact:write"]);

describe("ListActions", () => {
  it("renders only the in-scope action types (create/route/url), skipping the rest", async () => {
    const actions: ListAction[] = [
      { label: "New Contact", type: "create", view: "contacts_form" },
      { label: "Confirm", type: "route", route: "contacts.confirm" },
      { label: "Docs", type: "url", url: "https://example.com" },
      { label: "Export", type: "export" },
      { label: "Import", type: "import" },
    ];
    await renderActions(actions, fullAccess);

    expect(screen.getByText("New Contact")).toBeTruthy();
    expect(screen.getByText("Confirm")).toBeTruthy();
    expect(screen.getByText("Docs")).toBeTruthy();
    expect(screen.queryByText("Export")).toBeNull();
    expect(screen.queryByText("Import")).toBeNull();
  });

  it("hides an action the current user lacks permission for", async () => {
    const actions: ListAction[] = [
      { label: "New Contact", type: "create", view: "contacts_form", permission: "contacts:contact:write" },
    ];
    await renderActions(actions, permissionWrapper([]));

    expect(screen.queryByText("New Contact")).toBeNull();
  });

  it("route: hides an action the current user lacks permission for", async () => {
    const actions: ListAction[] = [
      { label: "Confirm", type: "route", route: "contacts.confirm", permission: "contacts:contact:write" },
    ];
    await renderActions(actions, permissionWrapper([]));

    expect(screen.queryByText("Confirm")).toBeNull();
  });

  it("url: hides an action the current user lacks permission for", async () => {
    const actions: ListAction[] = [
      { label: "Docs", type: "url", url: "https://example.com", permission: "contacts:contact:write" },
    ];
    await renderActions(actions, permissionWrapper([]));

    expect(screen.queryByText("Docs")).toBeNull();
  });

  it("route: disables the button while the mutation is pending", async () => {
    useActionMock.mockReturnValue({ mutate: vi.fn(), isPending: true, isError: false, error: null });
    const actions: ListAction[] = [{ label: "Confirm", type: "route", route: "contacts.confirm" }];
    await renderActions(actions, fullAccess);

    expect(screen.getByRole("button", { name: "Confirm" }).hasAttribute("disabled")).toBe(true);
  });

  it("create: resolves the view name to a path and navigates to its /_m browser link", async () => {
    const actions: ListAction[] = [{ label: "New Contact", type: "create", view: "contacts_form" }];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("New Contact"));
    await vi.waitFor(() => expect(resolveViewPathMock).toHaveBeenCalledWith("contacts_form", "contacts"));
  });

  it("create: surfaces an error instead of silently doing nothing when the view name doesn't resolve", async () => {
    resolveViewPathMock.mockResolvedValueOnce(null);
    const actions: ListAction[] = [{ label: "New Contact", type: "create", view: "missing_form" }];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("New Contact"));
    await vi.waitFor(() => expect(screen.getByRole("alert").textContent).toContain("missing_form"));
  });

  it("create: surfaces an error instead of an unhandled rejection when resolving the view fails", async () => {
    resolveViewPathMock.mockRejectedValueOnce(new Error("schema fetch failed"));
    const actions: ListAction[] = [{ label: "New Contact", type: "create", view: "contacts_form" }];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("New Contact"));
    await vi.waitFor(() => expect(screen.getByRole("alert").textContent).toBe("schema fetch failed"));
  });

  it("route: fires the mutation and surfaces its error", async () => {
    const mutate = vi.fn();
    useActionMock.mockReturnValue({ mutate, isPending: false, isError: true, error: { message: "boom" } });
    const actions: ListAction[] = [{ label: "Confirm", type: "route", route: "contacts.confirm" }];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Confirm"));
    expect(mutate).toHaveBeenCalledWith(undefined);
    expect(screen.getByRole("alert").textContent).toBe("boom");
  });

  it("url: renders an external link", async () => {
    const actions: ListAction[] = [{ label: "Docs", type: "url", url: "https://example.com" }];
    await renderActions(actions, fullAccess);

    const link = screen.getByText("Docs").closest("a");
    expect(link?.getAttribute("href")).toBe("https://example.com");
    expect(link?.getAttribute("target")).toBe("_blank");
  });

  it("url: requires a PermissionProvider even when the action itself has no `permission` set", () => {
    // Same fail-fast contract regardless of action type — a url action
    // with no `permission` field must still throw outside a provider,
    // matching create/route (which always require one via ActionButton).
    // Rendered directly (no router) since this doesn't need one and
    // RouterProvider's own error boundary would otherwise swallow the
    // throw into route-match state instead of propagating it.
    const actions: ListAction[] = [{ label: "Docs", type: "url", url: "https://example.com" }];
    expect(() => render(<ListActions actions={actions} module="contacts" />)).toThrow(
      /must be used within a PermissionProvider/,
    );
  });

  it("route: requires a PermissionProvider even for a malformed action missing its `route` field", () => {
    // The permission check must fire ahead of the `!action.route` check,
    // not be skipped by it — a malformed manifest action shouldn't mask a
    // missing PermissionProvider setup bug.
    const actions: ListAction[] = [{ label: "Confirm", type: "route" }];
    expect(() => render(<ListActions actions={actions} module="contacts" />)).toThrow(
      /must be used within a PermissionProvider/,
    );
  });
});
