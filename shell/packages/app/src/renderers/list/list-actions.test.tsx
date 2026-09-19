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
import { resetReportedConditionErrors } from "../../conditions/use-condition-evaluator.js";
import { ListActions } from "./list-actions.js";
import type { ListAction, Row } from "./list-view-types.js";

const { useActionMock, useExportMock, resolveViewPathMock, actionRegistryResolveMock, dispatchMock, toastErrorMock } =
  vi.hoisted(() => ({
    useActionMock: vi.fn(),
    useExportMock: vi.fn(),
    resolveViewPathMock: vi.fn(async (): Promise<string | null> => "/contacts/new"),
    actionRegistryResolveMock: vi.fn(async () => ({ method: "POST", path: "/contacts/duplicate" })),
    dispatchMock: vi.fn(async () => undefined),
    toastErrorMock: vi.fn(),
  }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return {
    ...actual,
    useAction: useActionMock,
    useExport: useExportMock,
    actionRegistry: { resolve: actionRegistryResolveMock },
    dispatch: dispatchMock,
  };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewPathRegistry: { resolve: resolveViewPathMock } };
});
vi.mock("@goerp/sdk/notifications", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/notifications")>();
  return { ...actual, toast: { ...actual.toast, error: toastErrorMock } };
});

beforeEach(() => {
  useActionMock.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false, error: null });
  useExportMock.mockReturnValue({
    trigger: vi.fn(async () => undefined),
    isPending: false,
    isError: false,
    error: null,
  });
});

afterEach(() => {
  cleanup();
  useActionMock.mockReset();
  useExportMock.mockReset();
  resolveViewPathMock.mockClear();
  actionRegistryResolveMock.mockClear();
  dispatchMock.mockClear();
  toastErrorMock.mockClear();
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

async function renderActions(
  actions: ListAction[],
  Wrapper: ({ children }: { children: ReactNode }) => ReactNode,
  props: { embedded?: boolean; showCreateAction?: boolean; record?: Row; viewName?: string } = {},
) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <Wrapper>
        <ListActions actions={actions} module="contacts" {...props} />
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
  it("renders only the in-scope action types (create/route/url/report/menu), skipping the rest", async () => {
    const actions: ListAction[] = [
      { label: "New Contact", type: "create", view: "contacts_form" },
      { label: "Confirm", type: "route", route: "contacts.confirm" },
      { label: "Docs", type: "url", url: "https://example.com" },
      { label: "Print", type: "report", report: "contacts.tax_invoice" },
      { label: "More", type: "menu", items: [] },
      { label: "Export", type: "export" },
      { label: "Import", type: "import" },
    ];
    await renderActions(actions, fullAccess);

    expect(screen.getByText("New Contact")).toBeTruthy();
    expect(screen.getByText("Confirm")).toBeTruthy();
    expect(screen.getByText("Docs")).toBeTruthy();
    expect(screen.getByText("Print")).toBeTruthy();
    expect(screen.getByText("More")).toBeTruthy();
    expect(screen.queryByText("Export")).toBeNull();
    expect(screen.queryByText("Import")).toBeNull();
  });

  it("hides a create action when embedded, per view-system.md's suppressed-actions contract", async () => {
    const actions: ListAction[] = [
      { label: "New Contact", type: "create", view: "contacts_form" },
      { label: "Docs", type: "url", url: "https://example.com" },
    ];
    await renderActions(actions, fullAccess, { embedded: true });

    expect(screen.queryByText("New Contact")).toBeNull();
    expect(screen.getByText("Docs")).toBeTruthy();
  });

  it("shows a create action while embedded when showCreateAction is set", async () => {
    const actions: ListAction[] = [{ label: "New Contact", type: "create", view: "contacts_form" }];
    await renderActions(actions, fullAccess, { embedded: true, showCreateAction: true });

    expect(screen.getByText("New Contact")).toBeTruthy();
  });

  it("shows a create action outside embedded mode regardless of showCreateAction", async () => {
    const actions: ListAction[] = [{ label: "New Contact", type: "create", view: "contacts_form" }];
    await renderActions(actions, fullAccess);

    expect(screen.getByText("New Contact")).toBeTruthy();
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

  it("route: passes route_params through to the mutation instead of dropping them", async () => {
    const mutate = vi.fn();
    useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
    const actions: ListAction[] = [
      { label: "Print", type: "route", route: "contacts.print", route_params: { format: "pdf" } },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Print"));
    expect(mutate).toHaveBeenCalledWith({ format: "pdf" });
  });

  it("route: confirm's collected input reaches the mutation under the declared field name", async () => {
    const mutate = vi.fn();
    useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
    const actions: ListAction[] = [
      {
        label: "Cancel",
        type: "route",
        route: "contacts.cancel",
        confirm: {
          title: "Cancel",
          message: "Why?",
          input: { field: "reason", label: "Reason", type: "text" },
        },
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Cancel"));
    const input = screen.getByLabelText("Reason");
    fireEvent.change(input, { target: { value: "duplicate" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(mutate).toHaveBeenCalledWith({ reason: "duplicate" });
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

  it("report: hides an action the current user lacks permission for", async () => {
    const actions: ListAction[] = [
      { label: "Print", type: "report", report: "contacts.tax_invoice", permission: "contacts:contact:write" },
    ];
    await renderActions(actions, permissionWrapper([]));

    expect(screen.queryByText("Print")).toBeNull();
  });

  it("report: triggers the export and surfaces its error", async () => {
    const trigger = vi.fn(async () => undefined);
    useExportMock.mockReturnValue({ trigger, isPending: false, isError: true, error: { message: "boom" } });
    const actions: ListAction[] = [{ label: "Print", type: "report", report: "contacts.tax_invoice" }];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Print"));
    expect(trigger).toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe("boom");
  });

  it("report: passes route_params through to the export trigger instead of dropping them", async () => {
    const trigger = vi.fn(async () => undefined);
    useExportMock.mockReturnValue({ trigger, isPending: false, isError: false, error: null });
    const actions: ListAction[] = [
      { label: "Print", type: "report", report: "contacts.tax_invoice", route_params: { lang: "en" } },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Print"));
    expect(trigger).toHaveBeenCalledWith({ lang: "en" });
  });

  it("route: confirm gates the mutation behind a dialog instead of firing immediately", async () => {
    const mutate = vi.fn();
    useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
    const actions: ListAction[] = [
      {
        label: "Cancel",
        type: "route",
        route: "contacts.cancel",
        confirm: { title: "Cancel Order", message: "This order will be cancelled." },
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByText("Cancel"));
    expect(mutate).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Cancel Order" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(mutate).toHaveBeenCalledWith(undefined);
  });

  it("menu: renders nested items and a separator, skipping permission-denied items", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          { label: "Duplicate", type: "route", route: "contacts.duplicate" },
          { type: "separator" },
          { label: "Hidden", type: "route", route: "contacts.hidden", permission: "contacts:contact:delete" },
        ],
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByText("Duplicate")).toBeTruthy();
    expect(screen.queryByText("Hidden")).toBeNull();
  });

  it("menu: firing a route item dispatches through the resolved action route", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [{ label: "Duplicate", type: "route", route: "contacts.duplicate", route_params: { id: "c1" } }],
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("Duplicate"));

    await vi.waitFor(() => expect(actionRegistryResolveMock).toHaveBeenCalledWith("contacts.duplicate"));
    expect(dispatchMock).toHaveBeenCalledWith(expect.anything(), "POST", "/contacts/duplicate", { id: "c1" });
  });

  it("menu: firing an item's action failure surfaces via toast, not an unhandled rejection", async () => {
    dispatchMock.mockRejectedValueOnce(new Error("route failed"));
    const actions: ListAction[] = [
      { label: "More", type: "menu", items: [{ label: "Duplicate", type: "route", route: "contacts.duplicate" }] },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("Duplicate"));

    await vi.waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith("route failed"));
  });

  it("menu: a create item whose view doesn't resolve surfaces via toast instead of silently doing nothing", async () => {
    resolveViewPathMock.mockResolvedValueOnce(null);
    const actions: ListAction[] = [
      { label: "More", type: "menu", items: [{ label: "New Contact", type: "create", view: "missing_form" }] },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("New Contact"));

    await vi.waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith(expect.stringContaining("missing_form")));
  });

  it("menu: an item's own confirm gates it behind a dialog before firing", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          {
            label: "Archive",
            type: "route",
            route: "contacts.archive",
            confirm: { title: "Archive Contact", message: "Archive this contact?" },
          },
        ],
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("Archive"));
    expect(dispatchMock).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Archive Contact" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await vi.waitFor(() => expect(dispatchMock).toHaveBeenCalled());
  });

  it("menu: an item's confirm input reaches the fired action under the declared field name", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          {
            label: "Archive",
            type: "route",
            route: "contacts.archive",
            confirm: { title: "Archive", message: "Why?", input: { field: "reason", label: "Reason", type: "text" } },
          },
        ],
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("Archive"));
    fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "stale" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await vi.waitFor(() =>
      expect(dispatchMock).toHaveBeenCalledWith(expect.anything(), "POST", "/contacts/duplicate", {
        reason: "stale",
      }),
    );
  });

  it("menu: sends no request body for a route item with no route_params and no confirm input", async () => {
    const actions: ListAction[] = [
      { label: "More", type: "menu", items: [{ label: "Duplicate", type: "route", route: "contacts.duplicate" }] },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByText("Duplicate"));

    await vi.waitFor(() =>
      expect(dispatchMock).toHaveBeenCalledWith(expect.anything(), "POST", "/contacts/duplicate", undefined),
    );
  });

  it("menu: filters out item types nothing fires (export/import/custom/nested menu), instead of a dead click", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          { label: "Duplicate", type: "route", route: "contacts.duplicate" },
          { label: "Export CSV", type: "export" },
          { label: "Nested", type: "menu", items: [] },
        ],
      },
    ];
    await renderActions(actions, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByText("Duplicate")).toBeTruthy();
    expect(screen.queryByText("Export CSV")).toBeNull();
    expect(screen.queryByText("Nested")).toBeNull();
  });

  it("menu: requires a PermissionProvider even when the action itself has no `permission` set", () => {
    const actions: ListAction[] = [{ label: "More", type: "menu", items: [] }];
    expect(() => render(<ListActions actions={actions} module="contacts" />)).toThrow(
      /must be used within a PermissionProvider/,
    );
  });
});

describe("ListActions conditions", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetReportedConditionErrors();
  });

  const confirm: ListAction = {
    label: "Confirm",
    type: "route",
    route: "sales.confirm",
    condition: "record.state = 'draft'",
  };

  it("shows an action whose condition holds against the record", async () => {
    await renderActions([confirm], fullAccess, { record: { state: "draft" } });
    expect(screen.getByRole("button", { name: "Confirm" })).toBeTruthy();
  });

  it("hides an action whose condition is false against the record", async () => {
    await renderActions([confirm], fullAccess, { record: { state: "done" } });
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("hides a record-bound action when there is no record, as on a list header", async () => {
    await renderActions([confirm], fullAccess);
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("evaluates a permission-based condition against the permission context", async () => {
    const actions: ListAction[] = [
      {
        label: "Allowed",
        type: "url",
        url: "https://a.example",
        condition: "user_has_permission('contacts:contact:write')",
      },
      {
        label: "Denied",
        type: "url",
        url: "https://b.example",
        condition: "user_has_permission('contacts:contact:delete')",
      },
    ];
    await renderActions(actions, fullAccess);
    expect(screen.getByText("Allowed")).toBeTruthy();
    expect(screen.queryByText("Denied")).toBeNull();
  });

  it("hides a menu's items individually by their own condition", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          { label: "Shown", type: "route", route: "contacts.a", condition: "record.state = 'draft'" },
          { label: "Concealed", type: "route", route: "contacts.b", condition: "record.state = 'done'" },
        ],
      },
    ];
    await renderActions(actions, fullAccess, { record: { state: "draft" } });
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByText("Shown")).toBeTruthy();
    expect(screen.queryByText("Concealed")).toBeNull();
  });

  it("drops a separator stranded by a hidden item, rather than leaving it dangling", async () => {
    const actions: ListAction[] = [
      {
        label: "More",
        type: "menu",
        items: [
          { label: "Shown", type: "route", route: "contacts.a" },
          { type: "separator" },
          { label: "Concealed", type: "route", route: "contacts.b", condition: "record.state = 'done'" },
        ],
      },
    ];
    await renderActions(actions, fullAccess, { record: { state: "draft" } });
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByText("Shown")).toBeTruthy();
    expect(screen.queryByRole("separator")).toBeNull();
  });

  it("hides a menu action itself by its own condition", async () => {
    await renderActions([{ label: "More", type: "menu", items: [], condition: "record.state = 'done'" }], fullAccess, {
      record: { state: "draft" },
    });
    expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  });

  it("hides an action, and reports the view and action, when its condition is malformed", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    await renderActions([{ ...confirm, condition: "record.state ==" }], fullAccess, {
      record: { state: "draft" },
      viewName: "orders_form",
    });
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
    const message = String(consoleError.mock.calls[0]?.[0]);
    expect(message).toContain("orders_form");
    expect(message).toContain('action "Confirm" condition');
  });
});
