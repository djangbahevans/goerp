import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
import type { Row } from "../list/list-view-types.js";
import { FormTabsRenderer, resolveRecordExpression } from "./form-tabs.js";
import type { FormTab } from "./form-view-types.js";

const { resolveViewMock, useInfiniteListMock, useRelationLabelsMock } = vi.hoisted(() => ({
  resolveViewMock: vi.fn(),
  useInfiniteListMock: vi.fn(),
  useRelationLabelsMock: vi.fn(() => new Map()),
}));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewDeclarationRegistry: { resolve: resolveViewMock } };
});
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock, useRelationLabels: useRelationLabelsMock };
});

afterEach(() => {
  cleanup();
  resolveViewMock.mockReset();
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

  it("view tab: shows a not-implemented message for a view type with no renderer yet", async () => {
    resolveViewMock.mockResolvedValue({
      name: "orders_kanban",
      type: "kanban",
      resource: "sales.order",
      label: "Orders",
    });
    await renderTabs([{ label: "Orders", type: "view", view: "sales.orders_kanban" }]);
    expect(await screen.findByText(/kanban.*isn't implemented yet/)).toBeTruthy();
  });
});
