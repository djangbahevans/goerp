import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { ResourceRegistryEntry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { FormTabsRenderer, resolveRecordExpression } from "./form-tabs.js";
import type { FormTab } from "./form-view-types.js";

const { resolveViewMock, resolveResourceMock, useInfiniteListMock, useRelationLabelsMock } = vi.hoisted(() => ({
  resolveViewMock: vi.fn(),
  // Every CRUD path present by default — a no-op for filterViewByCapability,
  // preserving this file's existing assertions about unfiltered view
  // content. Capability filtering itself is form-tabs.test.tsx's own
  // concern to cover, not every other test in this file's.
  resolveResourceMock: vi.fn<() => Promise<ResourceRegistryEntry | undefined>>(async () => ({
    module: "sales",
    resource: "sales.order",
    listPath: "/orders",
    getPath: "/orders/{id}",
    createPath: "/orders",
    updatePath: "/orders/{id}",
    deletePath: "/orders/{id}",
    pivotPath: null,
    listMethod: "GET",
    createMethod: "POST",
    updateMethod: "PUT",
    deleteMethod: "DELETE",
  })),
  useInfiniteListMock: vi.fn(),
  useRelationLabelsMock: vi.fn(() => new Map()),
}));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return {
    ...actual,
    viewDeclarationRegistry: { resolve: resolveViewMock },
    resourceRegistry: { resolve: resolveResourceMock },
  };
});
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock, useRelationLabels: useRelationLabelsMock };
});

afterEach(() => {
  cleanup();
  resolveViewMock.mockReset();
  resolveResourceMock.mockClear();
  useInfiniteListMock.mockReset();
});

describe("resolveRecordExpression", () => {
  it("resolves a record.{field} reference against the current record", () => {
    expect(resolveRecordExpression("record.id", { id: "01j" })).toBe("01j");
    expect(resolveRecordExpression("record.customer_id", { customer_id: "01k" })).toBe("01k");
  });

  it("passes through a literal string/number unchanged", () => {
    expect(resolveRecordExpression("active", {})).toBe("active");
    expect(resolveRecordExpression(42, {})).toBe(42);
  });
});

function permissionWrapper(permissions: string[]) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: { "contacts.contact": { notes: { read: true, write: true } } },
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

async function renderTabs(tabs: FormTab[], record: Row = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = permissionWrapper(["sales:order:read"]);
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <Wrapper>
          <FormTabsRenderer
            tabs={tabs}
            resource="contacts.contact"
            module="contacts"
            record={record}
            recordId="01j"
            onChange={vi.fn()}
            formReadonly={false}
          />
        </Wrapper>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("FormTabsRenderer", () => {
  beforeEach(() => {
    resolveViewMock.mockResolvedValue({ name: "orders_list", type: "list", resource: "sales.order", label: "Orders" });
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it("hides a tab the current user lacks permission for", async () => {
    await renderTabs([
      { label: "Orders", type: "view", view: "sales.orders_list", permission: "sales:order:read" },
      { label: "Restricted", type: "view", view: "sales.other", permission: "sales:order:missing" },
    ]);
    expect(screen.getByText("Orders")).toBeTruthy();
    expect(screen.queryByText("Restricted")).toBeNull();
  });

  it("never renders a restricted tab's content, even when it's the first (default-active) tab", async () => {
    resolveViewMock.mockImplementation(async (viewRef: string) =>
      viewRef === "sales.other"
        ? { name: "other", type: "custom", resource: "sales.other", label: "Restricted" }
        : { name: "orders_list", type: "list", resource: "sales.order", label: "Orders" },
    );
    await renderTabs([
      { label: "Restricted", type: "view", view: "sales.other", permission: "sales:order:missing" },
      { label: "Orders", type: "view", view: "sales.orders_list", permission: "sales:order:read" },
    ]);
    // The visible (and now default-active) tab is "Orders" — its own
    // content, not the restricted tab's, since the restricted tab was
    // filtered out before index 0 was ever assigned. The mocked
    // useInfiniteList resolves an empty page, so ListRenderer's own empty
    // state (EmptyState, list-renderer.md) is what confirms it actually
    // mounted and rendered "Orders"' content specifically.
    expect(screen.queryByText("Restricted")).toBeNull();
    expect(await screen.findByText("No orders found.")).toBeTruthy();
  });

  it("switches the visible tab content on click", async () => {
    await renderTabs([
      {
        label: "Fields",
        type: "fields",
        sections: [{ type: "fields", fields: [{ field: "notes", type: "textarea" }] }],
      },
      { label: "Custom", type: "component", component: "SomeWidget" },
    ]);
    expect(screen.getByLabelText("notes")).toBeTruthy();

    fireEvent.click(screen.getByText("Custom"));
    expect(screen.queryByLabelText("notes")).toBeNull();
    expect(await screen.findByText(/SomeWidget/)).toBeTruthy();
  });

  it("view tab: dispatches a resolved list-type view to ListRenderer", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "o1", state: "confirmed" }], meta: { cursor: null, hasMore: false } }] },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_list" }]);
    expect(await screen.findByRole("table", { name: "Orders" })).toBeTruthy();
  });

  it("view tab: applies capability gating (filterViewByCapability) the same as a full-page view — no List route means no table", async () => {
    resolveResourceMock.mockResolvedValueOnce(undefined); // resource unknown to the registry — no CRUD routes at all
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "o1", state: "confirmed" }], meta: { cursor: null, hasMore: false } }] },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_list" }]);
    await waitFor(() => expect(resolveResourceMock).toHaveBeenCalledWith("sales.order"));
    expect(screen.queryByRole("table", { name: "Orders" })).toBeNull();
  });

  it("view tab: shows a not-implemented message for a view type with no renderer yet", async () => {
    resolveViewMock.mockResolvedValue({
      name: "orders_form",
      type: "form",
      resource: "sales.order",
      label: "Orders",
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_form" }]);
    expect(await screen.findByText(/form.*isn't implemented yet/)).toBeTruthy();
  });

  it("view tab: dispatches a resolved kanban-type view to KanbanRenderer", async () => {
    resolveViewMock.mockResolvedValue({
      name: "orders_kanban",
      type: "kanban",
      resource: "sales.order",
      label: "Orders",
      group_by: "state",
      group_values: ["new", "done"],
      card_fields: ["reference"],
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_kanban" }]);
    expect(await screen.findByText("new")).toBeTruthy();
    expect(screen.getByText("done")).toBeTruthy();
  });

  it("view tab: dispatches a resolved calendar-type view to CalendarRenderer", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          {
            data: [{ id: "act-1", scheduled_at: "2026-05-13", subject: "Call Acme" }],
            meta: { cursor: null, hasMore: false },
          },
        ],
      },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
    resolveViewMock.mockResolvedValue({
      name: "activities_calendar",
      type: "calendar",
      resource: "sales.activity",
      label: "Activities",
      date_field: "scheduled_at",
      title_field: "subject",
      // Agenda ignores focusedDate/month windowing entirely, so the
      // mocked event renders regardless of what "today" happens to be.
      default_view: "agenda",
    });
    await renderTabs([{ label: "Activities", type: "view", view: "sales.activities_calendar" }]);
    expect(await screen.findByText("Call Acme")).toBeTruthy();
  });

  it("view tab: dispatches a resolved timeline-type view to TimelineRenderer", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          {
            data: [{ id: "task-1", planned_start: "2026-05-10", planned_end: "2026-05-14", name: "Design review" }],
            meta: { cursor: null, hasMore: false },
          },
        ],
      },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
    resolveViewMock.mockResolvedValue({
      name: "project_timeline",
      type: "timeline",
      resource: "project.task",
      label: "Timeline",
      start_field: "planned_start",
      end_field: "planned_end",
      label_field: "name",
    });
    await renderTabs([{ label: "Timeline", type: "view", view: "project.project_timeline" }]);
    // jsdom has no ResizeObserver, so the bar's rendered width (and thus its
    // visible label text) never resolves above the label-drop threshold —
    // the accessible name is what's reliably present regardless.
    expect(await screen.findByRole("group", { name: /Design review/ })).toBeTruthy();
  });

  it("view tab: shows a validation error and logs it, instead of crashing, when a resolved kanban view is missing a required field", async () => {
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    resolveViewMock.mockResolvedValue({
      name: "orders_kanban",
      type: "kanban",
      resource: "sales.order",
      label: "Orders",
      // group_by and card_fields, both required by manifest-spec.md §9.3,
      // are missing here.
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_kanban" }]);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("orders_kanban");
    expect(alert.textContent).toContain("kanban");
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("orders_kanban"));
    warnSpy.mockRestore();
  });
});
