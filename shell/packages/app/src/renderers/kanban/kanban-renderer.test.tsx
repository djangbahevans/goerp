import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import type { KanbanViewDeclaration } from "./kanban-manifest-types.js";
import { KanbanRenderer } from "./kanban-renderer.js";

const { useInfiniteListMock, saveRecordMock, resourceMetadataResolveMock, useRelationLabelsMock } = vi.hoisted(() => ({
  useInfiniteListMock: vi.fn(),
  saveRecordMock: vi.fn(),
  resourceMetadataResolveMock: vi.fn(),
  useRelationLabelsMock: vi.fn(() => new Map()),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return {
    ...actual,
    useInfiniteList: useInfiniteListMock,
    saveRecord: saveRecordMock,
    useRelationLabels: useRelationLabelsMock,
  };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, resourceMetadataRegistry: { resolve: resourceMetadataResolveMock } };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
  saveRecordMock.mockReset();
  resourceMetadataResolveMock.mockReset();
  useRelationLabelsMock.mockClear();
});

const view: KanbanViewDeclaration = {
  name: "leads_kanban",
  type: "kanban",
  resource: "crm.lead",
  label: "Pipeline",
  group_by: "stage",
  group_values: ["new", "won"],
  card_fields: ["display_name", "revenue"],
};

function pagedResult(rows: Row[], hasMore = false) {
  return {
    data: { pages: [{ data: rows, meta: { cursor: null, hasMore } }] },
    fetchNextPage: vi.fn(),
    hasNextPage: hasMore,
    isFetchingNextPage: false,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  };
}

async function renderKanbanRenderer(
  props: {
    embedded?: boolean;
    baseFilter?: Record<string, string>;
    recordId?: string;
    showCreateAction?: boolean;
  } = {},
  viewOverride: KanbanViewDeclaration = view,
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const permissionValue = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <QueryClientProvider client={queryClient}>
        <PermissionContext.Provider value={permissionValue}>
          <KanbanRenderer view={viewOverride} module="crm" {...props} />
        </PermissionContext.Provider>
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

describe("KanbanRenderer", () => {
  beforeEach(() => {
    resourceMetadataResolveMock.mockResolvedValue(undefined);
    saveRecordMock.mockResolvedValue({});
  });

  it("shows a loading state while records are in flight", async () => {
    useInfiniteListMock.mockReturnValue({
      data: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: true,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });

    await renderKanbanRenderer();

    expect(document.querySelector('[data-skeleton="card"]')).toBeTruthy();
  });

  it("shows an error state with a retry action", async () => {
    const refetch = vi.fn();
    useInfiniteListMock.mockReturnValue({
      data: undefined,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: true,
      error: new Error("boom"),
      refetch,
    });

    await renderKanbanRenderer();

    expect(screen.getByRole("alert").textContent).toContain("boom");
    fireEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalled();
  });

  it("renders a column per group_values entry with cards mapped from card_fields", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "lead-1", stage: "new", display_name: "Acme Corp", revenue: "$45,000" }]),
    );

    await renderKanbanRenderer();

    expect(screen.getByText("new")).toBeTruthy();
    expect(screen.getByText("won")).toBeTruthy();
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.getByText("$45,000")).toBeTruthy();
  });

  it("shows the empty state when there are no rows and no group_values declared", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));
    const { group_values: _groupValues, ...viewWithoutGroupValues } = view;

    await renderKanbanRenderer({}, viewWithoutGroupValues);

    expect(screen.getByRole("heading", { name: "No pipeline found." })).toBeTruthy();
  });

  it("drags a card to a new column, saving drag_updates_field (defaulting to group_by) on the dragged record", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "lead-1", stage: "new", display_name: "Acme Corp", revenue: "$45,000" }]),
    );

    await renderKanbanRenderer();
    const card = screen.getByRole("button", { name: "Acme Corp" });
    card.focus();
    fireEvent.keyDown(card, { key: " " });
    fireEvent.keyDown(card, { key: "ArrowRight" });
    fireEvent.keyDown(card, { key: " " });

    expect(saveRecordMock).toHaveBeenCalledWith("crm.lead", "lead-1", { stage: "won" });
  });

  it("uses drag_updates_field over group_by when both are declared", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "lead-1", stage: "new", display_name: "Acme Corp", revenue: "$45,000" }]),
    );

    await renderKanbanRenderer({}, { ...view, drag_updates_field: "pipeline_stage" });
    const card = screen.getByRole("button", { name: "Acme Corp" });
    card.focus();
    fireEvent.keyDown(card, { key: " " });
    fireEvent.keyDown(card, { key: "ArrowRight" });
    fireEvent.keyDown(card, { key: " " });

    expect(saveRecordMock).toHaveBeenCalledWith("crm.lead", "lead-1", { pipeline_stage: "won" });
  });

  it("quick-create: creates a record scoped to the column it was added from", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer({}, { ...view, quick_create: true });
    fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    const input = screen.getAllByPlaceholderText("Title")[0] as HTMLElement;
    fireEvent.change(input, { target: { value: "New Lead" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0] as HTMLElement);

    await waitFor(() =>
      expect(saveRecordMock).toHaveBeenCalledWith("crm.lead", undefined, { title: "New Lead", stage: "new" }),
    );
  });

  it("quick-create sets group_by, not drag_updates_field, when the two differ", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer({}, { ...view, quick_create: true, drag_updates_field: "pipeline_stage" });
    fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    const input = screen.getAllByPlaceholderText("Title")[0] as HTMLElement;
    fireEvent.change(input, { target: { value: "New Lead" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0] as HTMLElement);

    await waitFor(() =>
      expect(saveRecordMock).toHaveBeenCalledWith("crm.lead", undefined, { title: "New Lead", stage: "new" }),
    );
  });

  it("applies default_filters once on mount", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer({ embedded: true }, { ...view, default_filters: { state: "active" } });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "crm.lead",
      expect.objectContaining({ filter: expect.objectContaining({ state: "active" }) }),
    );
  });

  it("merges the embedded base filter and isolates the cache key by view and record", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer({ embedded: true, baseFilter: { customer_id: "acme" }, recordId: "01j" });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "crm.lead",
      expect.objectContaining({
        filter: expect.objectContaining({ customer_id: "acme" }),
        cacheKeyPrefix: "embedded:01j:leads_kanban",
      }),
    );
  });

  it("hides a create action when embedded, per view-system.md's suppressed-actions contract", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer(
      { embedded: true },
      { ...view, actions: [{ label: "New Lead", type: "create", view: "leads_form" }] },
    );

    expect(screen.queryByText("New Lead")).toBeNull();
  });

  it("shows a create action while embedded when the tab set show_create_action: true", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderKanbanRenderer(
      { embedded: true, showCreateAction: true },
      { ...view, actions: [{ label: "New Lead", type: "create", view: "leads_form" }] },
    );

    expect(screen.getByText("New Lead")).toBeTruthy();
  });

  it("hides a create-type column action when embedded, same as the header create action", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([{ id: "l1", stage: "new", display_name: "Lead 1" }]));

    await renderKanbanRenderer(
      { embedded: true },
      {
        ...view,
        group_values: ["new"],
        column_actions: [{ label: "New in column", type: "create", view: "leads_form" }],
      },
    );

    expect(screen.queryByText("New in column")).toBeNull();
  });

  it("shows a create-type column action while embedded when show_create_action: true", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([{ id: "l1", stage: "new", display_name: "Lead 1" }]));

    await renderKanbanRenderer(
      { embedded: true, showCreateAction: true },
      {
        ...view,
        group_values: ["new"],
        column_actions: [{ label: "New in column", type: "create", view: "leads_form" }],
      },
    );

    expect(screen.getByText("New in column")).toBeTruthy();
  });

  it("caps cards per column at max_cards_per_column, revealing more via Load more", async () => {
    const rows = Array.from({ length: 3 }, (_, i) => ({ id: `lead-${i}`, stage: "new", display_name: `Lead ${i}` }));
    useInfiniteListMock.mockReturnValue(pagedResult(rows));

    await renderKanbanRenderer({}, { ...view, max_cards_per_column: 2 });
    expect(screen.getByText("Lead 0")).toBeTruthy();
    expect(screen.getByText("Lead 1")).toBeTruthy();
    expect(screen.queryByText("Lead 2")).toBeNull();

    fireEvent.click(screen.getByText("Load more"));
    expect(screen.getByText("Lead 2")).toBeTruthy();
  });

  it("doesn't auto-fetch further pages on mount, only on-demand once a column exhausts what's already fetched", async () => {
    const rows = Array.from({ length: 2 }, (_, i) => ({ id: `lead-${i}`, stage: "new", display_name: `Lead ${i}` }));
    const result = pagedResult(rows, true);
    useInfiniteListMock.mockReturnValue(result);

    await renderKanbanRenderer({}, { ...view, max_cards_per_column: 2 });
    expect(result.fetchNextPage).not.toHaveBeenCalled();

    fireEvent.click(screen.getAllByText("Load more")[0] as HTMLElement);
    expect(result.fetchNextPage).toHaveBeenCalled();
  });

  it("resolves group_label_field/group_color_field via the group_by field's related resource", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([{ id: "lead-1", stage_id: "s1", display_name: "Acme Corp" }]));
    resourceMetadataResolveMock.mockResolvedValue({
      fields: [{ name: "stage_id", type: "relation", related_model: "crm.stage" }],
    });
    useRelationLabelsMock.mockReturnValue(
      new Map([
        ["group-label", { s1: "New" }],
        ["group-color", { s1: "#3B82F6" }],
      ]),
    );

    await renderKanbanRenderer(
      {},
      { ...view, group_by: "stage_id", group_values: ["s1"], group_label_field: "name", group_color_field: "color" },
    );

    expect(await screen.findByText("New")).toBeTruthy();
  });
});
