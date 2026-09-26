import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { UseSavedFiltersResult } from "@goerp/sdk/react";
import type { ViewExtensionEntry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { groupRows, ListRenderer, nextSortValue, rowClickHref, sortDirectionOf } from "./list-renderer.js";
import type { ListViewDeclaration, Row } from "./list-view-types.js";

const {
  useInfiniteListMock,
  useRelationLabelsMock,
  useSavedFiltersMock,
  resolveViewPathMock,
  resolveResourceMock,
  getMock,
  forTargetMock,
  extensionLoaderHasMock,
  extensionLoaderResolveMock,
} = vi.hoisted(() => ({
  useInfiniteListMock: vi.fn(),
  useRelationLabelsMock: vi.fn((_specs: { ids: string[] }[]) => new Map()),
  // No extensions target the current view by default — this file's own
  // "ListRenderer view extensions" describe block overrides per test.
  forTargetMock: vi.fn<() => Promise<ViewExtensionEntry[]>>(async () => []),
  extensionLoaderHasMock: vi.fn<(key: string) => boolean>(() => false),
  extensionLoaderResolveMock: vi.fn(),
  // No saved filters and already resolved by default — real network
  // access would otherwise hang indefinitely in this test environment
  // (apiClient's own internal retry logic, independent of TanStack
  // Query's retry option) since nothing here ever mocks the sdk's
  // internal http client. Tests exercising the saved-filter precedence
  // itself override this per-test.
  useSavedFiltersMock: vi.fn(
    (): UseSavedFiltersResult => ({
      filters: [],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    }),
  ),
  resolveViewPathMock: vi.fn(async (): Promise<string | null> => "/contacts/{id}"),
  // use-tree-rows.ts's own real (unmocked) data-fetching for children —
  // only exercised by tree_field tests, which set these explicitly.
  resolveResourceMock: vi.fn(async () => ({ listPath: "/contacts" })),
  getMock: vi.fn(
    async (): Promise<{ data: Row[]; meta: { cursor: null; hasMore: boolean } }> => ({
      data: [],
      meta: { cursor: null, hasMore: false },
    }),
  ),
}));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return {
    ...actual,
    useInfiniteList: useInfiniteListMock,
    useRelationLabels: useRelationLabelsMock,
    useSavedFilters: useSavedFiltersMock,
  };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return {
    ...actual,
    viewPathRegistry: { resolve: resolveViewPathMock, resolveRecord: resolveViewPathMock },
    resourceRegistry: { ...actual.resourceRegistry, resolve: resolveResourceMock },
    viewExtensionRegistry: { forTarget: forTargetMock },
  };
});
vi.mock("@goerp/sdk/module", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/module")>();
  return {
    ...actual,
    extensionBatchLoaderRegistry: { has: extensionLoaderHasMock, resolve: extensionLoaderResolveMock },
  };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
  useRelationLabelsMock.mockClear();
  useRelationLabelsMock.mockImplementation(() => new Map());
  useSavedFiltersMock.mockReset();
  useSavedFiltersMock.mockImplementation(() => ({
    filters: [],
    isLoading: false,
    save: vi.fn(),
    remove: vi.fn(),
    setDefault: vi.fn(),
  }));
  resolveViewPathMock.mockClear();
  resolveResourceMock.mockClear();
  getMock.mockReset();
  getMock.mockResolvedValue({ data: [], meta: { cursor: null, hasMore: false } });
  forTargetMock.mockReset();
  forTargetMock.mockResolvedValue([]);
  extensionLoaderHasMock.mockReset();
  extensionLoaderHasMock.mockReturnValue(false);
  extensionLoaderResolveMock.mockReset();
});

const view: ListViewDeclaration = {
  name: "contacts_list",
  type: "list",
  resource: "contacts.contact",
  label: "Contacts",
  columns: [
    { field: "name", label: "Name" },
    { field: "ssn", label: "SSN" },
  ],
  default_sort: "name",
};

function permissionWrapper(fieldAccess: Record<string, Record<string, { read: boolean; write: boolean }>>) {
  const value = createPermissionContextValue({ permissions: new Set(), fieldAccess, modulesEnabled: new Set() });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

async function renderListRenderer(
  props: {
    embedded?: boolean;
    baseFilter?: Record<string, string>;
    recordId?: string;
    module?: string;
    showCreateAction?: boolean;
  },
  Wrapper: ({ children }: { children: ReactNode }) => React.JSX.Element,
  initialPath = "/",
  viewOverride: ListViewDeclaration = view,
) {
  // useTreeRows calls the real useQueries unconditionally, so ListRenderer
  // needs a real QueryClient even with useInfiniteList/useRelationLabels mocked.
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <QueryClientProvider client={queryClient}>
        <Wrapper>
          <ListRenderer view={viewOverride} module="contacts" {...props} />
        </Wrapper>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return { router };
}

const fullAccess = permissionWrapper({
  "contacts.contact": { name: { read: true, write: true }, ssn: { read: true, write: true } },
});

describe("sortDirectionOf / nextSortValue", () => {
  it("reports undefined for an unsorted or differently-sorted column", () => {
    expect(sortDirectionOf(undefined, "name")).toBeUndefined();
    expect(sortDirectionOf("other", "name")).toBeUndefined();
  });

  it("reports asc for the bare field name, desc for the '-'-prefixed one", () => {
    expect(sortDirectionOf("name", "name")).toBe("asc");
    expect(sortDirectionOf("-name", "name")).toBe("desc");
  });

  it("cycles asc -> desc -> unsorted", () => {
    expect(nextSortValue(undefined, "name")).toBe("name");
    expect(nextSortValue("name", "name")).toBe("-name");
    expect(nextSortValue("-name", "name")).toBeUndefined();
  });

  it("starting a new column's sort doesn't matter what another column was sorted by", () => {
    expect(nextSortValue("-other", "name")).toBe("name");
  });
});

describe("rowClickHref", () => {
  it("returns undefined when there's no resolved path", () => {
    expect(rowClickHref(null, { id: "1" }, "id")).toBeUndefined();
  });

  it("substitutes the {id} token with the row's row_click_param field, module-linked", () => {
    expect(rowClickHref("/contacts/{id}", { id: "01j" }, "id")).toBe("/_m/contacts/01j");
  });

  it("reads the record id from a non-default row_click_param field", () => {
    expect(rowClickHref("/contacts/{id}", { id: "01j", uuid: "abc" }, "uuid")).toBe("/_m/contacts/abc");
  });

  it("returns undefined when the row_click_param field is missing or non-scalar", () => {
    expect(rowClickHref("/contacts/{id}", { name: "Ada" }, "id")).toBeUndefined();
    expect(rowClickHref("/contacts/{id}", { id: { nested: true } }, "id")).toBeUndefined();
  });
});

describe("groupRows", () => {
  it("returns a single ungrouped bucket when groupBy is unset", () => {
    const rows = [{ id: "1" }, { id: "2" }];
    expect(groupRows(rows, undefined)).toEqual([{ key: "", rows }]);
  });

  it("buckets rows by the field's value, preserving first-seen order", () => {
    const rows = [
      { id: "1", state: "draft" },
      { id: "2", state: "done" },
      { id: "3", state: "draft" },
    ];
    expect(groupRows(rows, "state")).toEqual([
      { key: "draft", rows: [rows[0], rows[2]] },
      { key: "done", rows: [rows[1]] },
    ]);
  });
});

describe("ListRenderer", () => {
  it("shows a loading state while the initial page is in flight", async () => {
    useInfiniteListMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      isFetchingNextPage: true,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    expect(document.querySelector('[data-skeleton="table"]')).toBeTruthy();
  });

  it("shows an error state with a retry action", async () => {
    const refetch = vi.fn();
    useInfiniteListMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch,
      error: new Error("boom"),
    });

    await renderListRenderer({}, fullAccess);

    expect(screen.getByRole("alert").textContent).toContain("boom");
    screen.getByText("Retry").click();
    expect(refetch).toHaveBeenCalled();
  });

  it("shows an empty state when the resolved page has no rows", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    expect(screen.getByRole("heading", { name: "No contacts found." })).toBeTruthy();
  });

  it("renders rows and hides a column the user lacks field-level read access to", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada", ssn: "000-00-0000" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer(
      {},
      permissionWrapper({
        "contacts.contact": { name: { read: true, write: true }, ssn: { read: false, write: false } },
      }),
    );

    const table = screen.getByRole("table", { name: "Contacts" });
    expect(within(table).getByText("Name")).toBeTruthy();
    expect(within(table).queryByText("SSN")).toBeNull();
    expect(within(table).getByText("Ada")).toBeTruthy();
    expect(within(table).queryByText("000-00-0000")).toBeNull();
  });

  it("has no Columns toggle when the view declares no hidden columns", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada", ssn: "000-00-0000" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    expect(screen.queryByRole("button", { name: "Columns" })).toBeNull();
  });

  it("Columns toggle reveals a hidden: true column into the table", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada", internal_note: "VIP" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer(
      {},
      permissionWrapper({
        "contacts.contact": {
          name: { read: true, write: true },
          ssn: { read: true, write: true },
          internal_note: { read: true, write: true },
        },
      }),
      "/",
      {
        ...view,
        columns: [...(view.columns ?? []), { field: "internal_note", label: "Internal Note", hidden: true }],
      },
    );

    const table = screen.getByRole("table", { name: "Contacts" });
    expect(within(table).queryByText("Internal Note")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Columns" }));
    const menuItem = screen.getByRole("menuitemcheckbox", { name: "Internal Note" });
    expect(menuItem.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(menuItem);

    expect(within(table).getByText("Internal Note")).toBeTruthy();
    expect(within(table).getByText("VIP")).toBeTruthy();
  });

  it("has no checkbox column when the view declares no bulk_actions", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada", ssn: "000-00-0000" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    expect(screen.queryAllByRole("checkbox").length).toBe(0);
  });

  it("renders a checkbox column and toggles row selection when bulk_actions are declared", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          {
            data: [
              { id: "1", name: "Ada", ssn: "000-00-0000" },
              { id: "2", name: "Bea", ssn: "111-11-1111" },
            ],
            meta: { cursor: null, hasMore: false },
          },
        ],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      bulk_actions: [{ label: "Add Tag", type: "custom", component: "BulkTagAction" }],
    });

    const rowCheckboxes = screen.getAllByRole("checkbox", { name: "Select row" });
    expect(rowCheckboxes).toHaveLength(2);
    expect(screen.queryByText("1 selected")).toBeNull();

    const selectAll = () => screen.getByRole("checkbox", { name: /Select all/ }) as HTMLInputElement;
    expect(selectAll().indeterminate).toBe(false);

    fireEvent.click(rowCheckboxes[0] as HTMLInputElement);
    expect(screen.getByText("1 selected")).toBeTruthy();
    expect(selectAll().indeterminate).toBe(true);
    expect(selectAll().getAttribute("aria-checked")).toBe("mixed");

    fireEvent.click(selectAll());
    expect(screen.getByText("2 selected")).toBeTruthy();
    expect(selectAll().checked).toBe(true);
    expect(selectAll().indeterminate).toBe(false);

    fireEvent.click(screen.getByRole("checkbox", { name: /Select all/ }));
    expect(screen.queryByText(/selected/)).toBeNull();
  });

  it("renders no checkbox column when the view sets selectable to false", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada", ssn: "000-00-0000" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      selectable: false,
      bulk_actions: [{ label: "Add Tag", type: "custom", component: "BulkTagAction" }],
    });

    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
  });

  describe("bulk action conditions", () => {
    function rows() {
      useInfiniteListMock.mockReturnValue({
        data: {
          pages: [{ data: [{ id: "1", name: "Ada", ssn: "000-00-0000" }], meta: { cursor: null, hasMore: false } }],
        },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });
    }

    it("shows a bulk action whose condition holds", async () => {
      rows();
      await renderListRenderer({}, fullAccess, "/", {
        ...view,
        bulk_actions: [
          {
            label: "Export",
            type: "export",
            route: "contacts.exportContacts",
            condition: "NOT user_has_permission('contacts:contact:export')",
          },
        ],
      });
      fireEvent.click(screen.getByRole("checkbox", { name: "Select row" }));
      expect(screen.getByRole("button", { name: "Export" })).toBeTruthy();
    });

    it("renders no selection checkboxes when every bulk action's condition is false", async () => {
      rows();
      await renderListRenderer({}, fullAccess, "/", {
        ...view,
        bulk_actions: [
          {
            label: "Add Tag",
            type: "custom",
            component: "BulkTagAction",
            condition: "user_has_permission('contacts:contact:export')",
          },
        ],
      });
      expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    });

    it("hides only the bulk action whose condition is false, keeping selection for the rest", async () => {
      rows();
      await renderListRenderer({}, fullAccess, "/", {
        ...view,
        bulk_actions: [
          { label: "Export", type: "export", route: "contacts.exportContacts" },
          {
            label: "Export Hidden",
            type: "export",
            route: "contacts.exportContacts",
            condition: "user_has_role('admin')",
          },
        ],
      });
      fireEvent.click(screen.getByRole("checkbox", { name: "Select row" }));
      expect(screen.getByRole("button", { name: "Export" })).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Export Hidden" })).toBeNull();
    });
  });

  it("full-page mode: passes the URL-derived filter/sort into useInfiniteList", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/?filter[is_active]=true&sort=-created_at");

    expect(useInfiniteListMock).toHaveBeenCalledWith("contacts.contact", {
      filter: { is_active: true },
      sort: "-created_at",
    });
  });

  it("combines default_sort with default_sort_dir into the initial sort string", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", { ...view, default_sort: "created_at", default_sort_dir: "desc" });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.contact",
      expect.objectContaining({ sort: "-created_at" }),
    );
  });

  it("embedded mode: merges baseFilter with local state, defaults sort from the view, ignores the URL, and scopes the cache key to the parent record", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer(
      { embedded: true, baseFilter: { parent_id: "42" }, recordId: "contact-1" },
      fullAccess,
      "/?filter[is_active]=true",
    );

    expect(useInfiniteListMock).toHaveBeenCalledWith("contacts.contact", {
      filter: { parent_id: "42" },
      sort: "name",
      cacheKeyPrefix: "embedded:contact-1:contacts_list",
    });
  });

  it("the locked base filter always wins over a colliding user-driven filter value", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({ baseFilter: { parent_id: "locked" } }, fullAccess, "/?filter[parent_id]=user-supplied");

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.contact",
      expect.objectContaining({ filter: { parent_id: "locked" } }),
    );
  });

  it("renders a group-by select from group_by_options and splits rows into grouped tables on selection", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          {
            data: [
              { id: "1", name: "Ada", state: "draft" },
              { id: "2", name: "Bea", state: "done" },
            ],
            meta: { cursor: null, hasMore: false },
          },
        ],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/?group_by=state", { ...view, group_by_options: ["state"] });

    // Select (select.tsx) is a Radix combobox trigger, not a native
    // <select> — its current value shows as the trigger's own text.
    expect(screen.getByRole("combobox").textContent).toBe("state");
    expect(screen.getAllByRole("table")).toHaveLength(2);
    expect(screen.getByText("state = draft")).toBeTruthy();
    expect(screen.getByText("state = done")).toBeTruthy();
  });

  it("still shows a group caption for a bucket whose grouped field is empty/missing", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          {
            data: [
              { id: "1", name: "Ada", state: "draft" },
              { id: "2", name: "Bea" },
            ],
            meta: { cursor: null, hasMore: false },
          },
        ],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/?group_by=state", { ...view, group_by_options: ["state"] });

    expect(screen.getAllByRole("table")).toHaveLength(2);
    expect(screen.getByText("state = draft")).toBeTruthy();
    expect(screen.getByText("state =")).toBeTruthy();
  });

  it("renders in-scope filters and actions from the view declaration", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      filters: [{ field: "is_active", label: "Active", type: "boolean" }],
      actions: [{ label: "New Contact", type: "create", view: "contacts_form" }],
    });

    expect(screen.getByText("Active")).toBeTruthy();
    expect(screen.getByText("New Contact")).toBeTruthy();
  });

  it("hides a create action when embedded, per view-system.md's suppressed-actions contract", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({ embedded: true }, fullAccess, "/", {
      ...view,
      actions: [{ label: "New Contact", type: "create", view: "contacts_form" }],
    });

    expect(screen.queryByText("New Contact")).toBeNull();
  });

  it("shows a create action while embedded when the tab set show_create_action: true", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({ embedded: true, showCreateAction: true }, fullAccess, "/", {
      ...view,
      actions: [{ label: "New Contact", type: "create", view: "contacts_form" }],
    });

    expect(screen.getByText("New Contact")).toBeTruthy();
  });

  it("disables the saved-filters fetch when embedded, since it's never consulted there", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({ embedded: true }, fullAccess);

    expect(useSavedFiltersMock).toHaveBeenCalledWith("contacts_list", { enabled: false });
  });

  it("requests batch-fetched relation labels for a relation column with no display_field", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", customer_id: "c1" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const wrapper = permissionWrapper({ "contacts.contact": { customer_id: { read: true, write: true } } });
    await renderListRenderer({}, wrapper, "/", {
      ...view,
      columns: [{ field: "customer_id", type: "relation", resource: "sales.customer", resource_label_field: "name" }],
    });

    expect(useRelationLabelsMock).toHaveBeenCalledWith([
      {
        key: "customer_id",
        resource: "sales.customer",
        labelField: "name",
        view: "contacts.contacts_list",
        ids: ["c1"],
      },
    ]);
  });

  it("requests batch-fetched relation labels for a relation column with no resource_label_field (registry resolves the default)", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", customer_id: "c1" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const wrapper = permissionWrapper({ "contacts.contact": { customer_id: { read: true, write: true } } });
    await renderListRenderer({}, wrapper, "/", {
      ...view,
      columns: [{ field: "customer_id", type: "relation", resource: "sales.customer" }],
    });

    expect(useRelationLabelsMock).toHaveBeenCalledWith([
      { key: "customer_id", resource: "sales.customer", view: "contacts.contacts_list", ids: ["c1"] },
    ]);
  });

  it("builds one relation-label spec per already-fetched page, so an earlier page's ids are never re-requested", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [
          { data: [{ id: "1", customer_id: "c1" }], meta: { cursor: "p2", hasMore: true } },
          { data: [{ id: "2", customer_id: "c2" }], meta: { cursor: null, hasMore: false } },
        ],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: true,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const wrapper = permissionWrapper({ "contacts.contact": { customer_id: { read: true, write: true } } });
    await renderListRenderer({}, wrapper, "/", {
      ...view,
      columns: [{ field: "customer_id", type: "relation", resource: "sales.customer", resource_label_field: "name" }],
    });

    expect(useRelationLabelsMock).toHaveBeenCalledWith([
      {
        key: "customer_id",
        resource: "sales.customer",
        labelField: "name",
        view: "contacts.contacts_list",
        ids: ["c1"],
      },
      {
        key: "customer_id",
        resource: "sales.customer",
        labelField: "name",
        view: "contacts.contacts_list",
        ids: ["c2"],
      },
    ]);
  });

  it("applies default_filters and each filter's own default when the URL has no filter[...] params", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const { router } = await renderListRenderer({}, fullAccess, "/", {
      ...view,
      default_filters: { is_active: true },
      filters: [{ field: "type", label: "Type", type: "select", default: "person" }],
    });

    expect(router.state.location.search).toEqual({
      "filter[is_active]": true,
      "filter[type]": "person",
    });
  });

  it("does not override filters already present in the URL with declared defaults", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const { router } = await renderListRenderer({}, fullAccess, "/?filter[type]=company", {
      ...view,
      filters: [{ field: "type", label: "Type", type: "select", default: "person" }],
    });

    expect(router.state.location.search).toEqual({ "filter[type]": "company" });
  });

  it("applies the user's own is_default saved filter instead of the manifest's default_filters", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "Mine",
          queryString: "?filter[is_active]=false",
          isDefault: true,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    const { router } = await renderListRenderer({}, fullAccess, "/", {
      ...view,
      default_filters: { is_active: true },
    });

    expect(router.state.location.search).toEqual({ "filter[is_active]": false });
  });

  it("explicit URL params win over the user's own is_default saved filter", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "Mine",
          queryString: "?filter[is_active]=false",
          isDefault: true,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    const { router } = await renderListRenderer({}, fullAccess, "/?filter[type]=company", view);

    expect(router.state.location.search).toEqual({ "filter[type]": "company" });
  });

  it("an explicit URL sort with no filter[...] params still wins over a saved default's sort", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });
    useSavedFiltersMock.mockReturnValue({
      filters: [{ id: "f1", viewName: "contacts_list", label: "Mine", queryString: "?sort=name", isDefault: true }],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    const { router } = await renderListRenderer({}, fullAccess, "/?sort=-created_at", view);

    expect(router.state.location.search).toEqual({ sort: "-created_at" });
  });

  it("waits for the saved-filters fetch to resolve before applying the manifest's default_filters", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });
    useSavedFiltersMock.mockReturnValue({
      filters: [],
      isLoading: true,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    const { router } = await renderListRenderer({}, fullAccess, "/", {
      ...view,
      default_filters: { is_active: true },
    });

    expect(router.state.location.search).toEqual({});
  });

  it("Load more: shows ActionButton's loading treatment while isFetchingNextPage", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: "p2", hasMore: true } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: true,
      hasNextPage: true,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    expect(screen.getByRole("button", { name: "Load more" }).getAttribute("aria-busy")).toBe("true");
  });

  it("Load more: calls fetchNextPage on click once no longer fetching", async () => {
    const fetchNextPage = vi.fn();
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: "p2", hasMore: true } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: true,
      fetchNextPage,
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess);

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(fetchNextPage).toHaveBeenCalled();
  });

  it("sortable header: clicking cycles asc -> desc -> unsorted, updating aria-sort each time", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      name: view.name,
      type: view.type,
      resource: view.resource,
      label: view.label,
      columns: [{ field: "name", label: "Name", sortable: true }],
    });

    expect(screen.getByRole("columnheader").getAttribute("aria-sort")).toBe("none");

    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    await waitFor(() =>
      expect(useInfiniteListMock).toHaveBeenLastCalledWith(
        "contacts.contact",
        expect.objectContaining({ sort: "name" }),
      ),
    );
    expect(screen.getByRole("columnheader").getAttribute("aria-sort")).toBe("ascending");

    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    await waitFor(() =>
      expect(useInfiniteListMock).toHaveBeenLastCalledWith(
        "contacts.contact",
        expect.objectContaining({ sort: "-name" }),
      ),
    );
    expect(screen.getByRole("columnheader").getAttribute("aria-sort")).toBe("descending");

    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    await waitFor(() => expect(screen.getByRole("columnheader").getAttribute("aria-sort")).toBe("none"));
  });

  it("row_click: clicking a row with no selection checkbox navigates to the resolved, id-substituted path", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    const { router } = await renderListRenderer({}, fullAccess, "/", { ...view, row_click: "contacts_form" });

    await waitFor(() => expect(resolveViewPathMock).toHaveBeenCalledWith("contacts_form", "contacts"));

    const row = await screen.findByRole("row", { name: /Ada/ });
    expect(row.getAttribute("tabindex")).toBe("0");
    fireEvent.click(row);

    await waitFor(() => expect(router.state.location.pathname).toBe("/_m/contacts/1"));
  });

  it("row_click: with a selection checkbox present, the primary column becomes the link instead of a row-level click target", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      row_click: "contacts_form",
      columns: [{ field: "name", label: "Name", primary: true }],
      bulk_actions: [{ label: "Export", type: "export", route: "contacts.exportContacts" }],
    });

    const link = await screen.findByRole("link", { name: "Ada" });
    expect(link.getAttribute("href")).toBe("/_m/contacts/1");

    const row = screen.getByRole("row", { name: /Ada/ });
    expect(row.getAttribute("tabindex")).toBeNull();
  });

  it("selected row: bg-primary-subtle wins, not layered alongside the default bg-surface", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      bulk_actions: [{ label: "Export", type: "export", route: "contacts.exportContacts" }],
    });

    const row = screen.getByRole("row", { name: /Ada/ });
    expect(row.className).toContain("bg-surface");
    expect(row.className).not.toContain("bg-primary-subtle");

    fireEvent.click(screen.getByRole("checkbox", { name: "Select row" }));

    expect(row.className).toContain("bg-primary-subtle");
    expect(row.className).not.toContain("bg-surface");
  });

  it("row_click: a primary column that already renders its own link (e.g. email) isn't wrapped in a second, nesting <a>", async () => {
    useInfiniteListMock.mockReturnValue({
      data: {
        pages: [{ data: [{ id: "1", email: "ada@example.com" }], meta: { cursor: null, hasMore: false } }],
      },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer(
      {},
      permissionWrapper({ "contacts.contact": { email: { read: true, write: true } } }),
      "/",
      {
        ...view,
        row_click: "contacts_form",
        columns: [{ field: "email", label: "Email", type: "email", primary: true }],
        bulk_actions: [{ label: "Export", type: "export", route: "contacts.exportContacts" }],
      },
    );

    await waitFor(() => expect(resolveViewPathMock).toHaveBeenCalled());

    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]?.getAttribute("href")).toBe("mailto:ada@example.com");
  });

  describe("tree_field", () => {
    const treeView: ListViewDeclaration = {
      ...view,
      columns: [{ field: "name", label: "Name" }],
      tree_field: "parent_id",
    };

    it("fetches root rows with an isnull filter on the tree field", async () => {
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "r1", name: "Root" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });

      await renderListRenderer({}, fullAccess, "/", treeView);

      expect(useInfiniteListMock).toHaveBeenCalledWith(
        "contacts.contact",
        expect.objectContaining({ filter: { parent_id: { isnull: true } } }),
      );
    });

    it("keeps root pages as separate relation-label specs, not merged with expanded children", async () => {
      useInfiniteListMock.mockReturnValue({
        data: {
          pages: [
            { data: [{ id: "r1", customer_id: "c1" }], meta: { cursor: "p2", hasMore: true } },
            { data: [{ id: "r2", customer_id: "c2" }], meta: { cursor: null, hasMore: false } },
          ],
        },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: true,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });
      getMock.mockResolvedValue({ data: [{ id: "c1a", customer_id: "c3" }], meta: { cursor: null, hasMore: false } });

      const wrapper = permissionWrapper({ "contacts.contact": { customer_id: { read: true, write: true } } });
      await renderListRenderer({}, wrapper, "/", {
        ...treeView,
        columns: [{ field: "customer_id", type: "relation", resource: "sales.customer", resource_label_field: "name" }],
      });

      fireEvent.click(screen.getAllByRole("button", { name: "Expand" })[0] as HTMLButtonElement);
      await waitFor(() => expect(getMock).toHaveBeenCalled());

      const specs = useRelationLabelsMock.mock.calls.at(-1)?.[0] ?? [];
      // One spec per root page (["c1"], ["c2"]) plus one for the expanded
      // node's own children ("c3") — never one spec merging all three, which
      // would re-request "c1"/"c2" every time expansion elsewhere changes.
      expect(specs.map((s) => s.ids)).toEqual([["c1"], ["c2"], ["c3"]]);
    });

    it("respects an embedded baseFilter that already scopes the tree field, instead of forcing isnull", async () => {
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "c1", name: "Child" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });

      // view-system.md's embedded-rendering contract: a locked baseFilter
      // always wins — here it scopes the tree to a specific parent's
      // subtree rather than the resource's true roots.
      await renderListRenderer({ embedded: true, baseFilter: { parent_id: "r1" } }, fullAccess, "/", treeView);

      expect(useInfiniteListMock).toHaveBeenCalledWith(
        "contacts.contact",
        expect.objectContaining({ filter: { parent_id: "r1" } }),
      );
    });

    it("shows an expand chevron for a root row, and reveals its children on click", async () => {
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "r1", name: "Root" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });
      getMock.mockResolvedValue({ data: [{ id: "c1", name: "Child" }], meta: { cursor: null, hasMore: false } });

      await renderListRenderer({}, fullAccess, "/", treeView);

      // role="treegrid" (list-renderer.md's Accessibility section), not
      // "table" — a tree_field view's table carries a different accessible
      // role than a flat list's.
      const table = screen.getByRole("treegrid", { name: "Contacts" });
      expect(within(table).queryByText("Child")).toBeNull();

      const rootRow = screen.getByText("Root").closest("tr") as HTMLTableRowElement;
      expect(rootRow.getAttribute("aria-level")).toBe("1");
      expect(rootRow.getAttribute("aria-expanded")).toBe("false");

      fireEvent.click(screen.getByRole("button", { name: "Expand" }));

      await waitFor(() => expect(within(table).getByText("Child")).toBeTruthy());
      // treeView's base sort (view.default_sort: "name") applies to the
      // children fetch too, same as any other filter/sort a flat list on
      // this view would already send.
      expect(getMock).toHaveBeenCalledWith("/contacts", { params: { "filter[parent_id]": "r1", sort: "name" } });
      expect(rootRow.getAttribute("aria-expanded")).toBe("true");
      expect(screen.getByText("Child").closest("tr")?.getAttribute("aria-level")).toBe("2");
    });

    it("shows no expand affordance once a row is confirmed to have no children", async () => {
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "r1", name: "Root" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });
      getMock.mockResolvedValue({ data: [], meta: { cursor: null, hasMore: false } });

      await renderListRenderer({}, fullAccess, "/", { ...treeView, default_expanded_depth: 1 });

      await waitFor(() => expect(getMock).toHaveBeenCalled());
      expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Collapse" })).toBeNull();
    });

    it("shows a retry affordance when a row's children fetch fails, and re-fetches on click", async () => {
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "r1", name: "Root" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });
      getMock.mockRejectedValueOnce(new Error("network error"));

      await renderListRenderer({}, fullAccess, "/", treeView);
      fireEvent.click(screen.getByRole("button", { name: "Expand" }));

      await waitFor(() => expect(screen.getByText("Couldn't load these rows.")).toBeTruthy());

      getMock.mockResolvedValueOnce({ data: [{ id: "c1", name: "Child" }], meta: { cursor: null, hasMore: false } });
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));

      await waitFor(() => expect(screen.getByText("Child")).toBeTruthy());
    });

    it("warns when both tree_field and group_by_options are declared on the same view", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      useInfiniteListMock.mockReturnValue({
        data: { pages: [{ data: [{ id: "r1", name: "Root" }], meta: { cursor: null, hasMore: false } }] },
        isLoading: false,
        isError: false,
        isFetchingNextPage: false,
        hasNextPage: false,
        fetchNextPage: vi.fn(),
        refetch: vi.fn(),
        error: null,
      });

      await renderListRenderer({}, fullAccess, "/", { ...treeView, group_by_options: ["state"] });

      await waitFor(() =>
        expect(warn).toHaveBeenCalledWith(expect.stringContaining("tree_field and group_by_options")),
      );
      expect(screen.queryByText("Group by")).toBeNull();

      warn.mockRestore();
    });
  });

  it("row_click: warns when a selection checkbox is present but no column is marked primary", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetchingNextPage: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer({}, fullAccess, "/", {
      ...view,
      row_click: "contacts_form",
      columns: [{ field: "name", label: "Name" }],
      bulk_actions: [{ label: "Export", type: "export", route: "contacts.exportContacts" }],
    });

    await waitFor(() => expect(warn).toHaveBeenCalledWith(expect.stringContaining("no column marked primary: true")));
    expect(screen.queryByRole("link", { name: "Ada" })).toBeNull();

    warn.mockRestore();
  });
});

describe("ListRenderer view extensions", () => {
  function permissionWrapperWithPermission(permissions: string[]) {
    const value = createPermissionContextValue({
      permissions: new Set(permissions),
      fieldAccess: { "contacts.contact": { name: { read: true, write: true }, ssn: { read: true, write: true } } },
      modulesEnabled: new Set(),
    });
    return function Wrapper({ children }: { children: ReactNode }) {
      return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
    };
  }

  const oneRow = {
    data: { pages: [{ data: [{ id: "1", name: "Ada" }], meta: { cursor: null, hasMore: false } }] },
    isLoading: false,
    isError: false,
    isFetchingNextPage: false,
    hasNextPage: false,
    fetchNextPage: vi.fn(),
    refetch: vi.fn(),
    error: null,
  };

  it("columns: appends a permitted extension column after the view's own, populated from its batch loader", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_department_column" },
        definition: {
          name: "hr_department_column",
          type: "columns",
          target_section: "columns",
          position: "append",
          columns: [{ field: "hr:department", label: "Department", type: "text", permission: "hr:employee:read" }],
        },
      },
    ]);
    extensionLoaderHasMock.mockImplementation((key: string) => key === "contacts.contacts_list");
    extensionLoaderResolveMock.mockReturnValue(async () => new Map([["1", { "hr:department": "Engineering" }]]));

    await renderListRenderer({}, permissionWrapperWithPermission(["hr:employee:read"]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    await screen.findByRole("columnheader", { name: "Department" });
    const headers = screen.getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual(["Name", "Department"]);
    await waitFor(() => expect(extensionLoaderResolveMock).toHaveBeenCalledWith("contacts.contacts_list"));
    expect(await screen.findByRole("cell", { name: "Engineering" })).toBeTruthy();
  });

  it("columns: a relation-type extension column renders the batch loader's own {id, display} value directly", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_manager_column" },
        definition: {
          name: "hr_manager_column",
          type: "columns",
          target_section: "columns",
          position: "append",
          columns: [{ field: "hr:manager_id", label: "Manager", type: "relation", permission: "hr:employee:read" }],
        },
      },
    ]);
    extensionLoaderHasMock.mockImplementation((key: string) => key === "contacts.contacts_list");
    extensionLoaderResolveMock.mockReturnValue(
      async () => new Map([["1", { "hr:manager_id": { id: "e42", display: "Grace Hopper" } }]]),
    );

    await renderListRenderer({}, permissionWrapperWithPermission(["hr:employee:read"]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    expect(await screen.findByRole("cell", { name: "Grace Hopper" })).toBeTruthy();
  });

  it("columns: does not render an extension column the current user lacks permission for, and never calls its loader", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_department_column" },
        definition: {
          name: "hr_department_column",
          type: "columns",
          target_section: "columns",
          position: "append",
          columns: [{ field: "hr:department", label: "Department", type: "text", permission: "hr:employee:read" }],
        },
      },
    ]);
    extensionLoaderHasMock.mockImplementation((key: string) => key === "contacts.contacts_list");
    extensionLoaderResolveMock.mockReturnValue(async () => new Map([["1", { "hr:department": "Engineering" }]]));

    await renderListRenderer({}, permissionWrapperWithPermission([]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    const headers = await screen.findAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual(["Name"]);
    expect(extensionLoaderResolveMock).not.toHaveBeenCalled();
  });

  it("columns: renders unchanged when the extending module isn't loaded (no matching batch loader registered)", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_department_column" },
        definition: {
          name: "hr_department_column",
          type: "columns",
          target_section: "columns",
          position: "append",
          columns: [{ field: "hr:department", label: "Department", type: "text", permission: "hr:employee:read" }],
        },
      },
    ]);
    extensionLoaderHasMock.mockReturnValue(false); // module not loaded — no loader registered

    await renderListRenderer({}, permissionWrapperWithPermission(["hr:employee:read"]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    await screen.findByRole("columnheader", { name: "Department" });
    expect(extensionLoaderResolveMock).not.toHaveBeenCalled();
    // The column renders (namespaced field, unresolved) but blank — no crash.
    expect(screen.queryByRole("cell", { name: "Engineering" })).toBeNull();
  });

  it("filter: renders a permitted extension filter in the filter panel", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_department_filter" },
        definition: {
          name: "hr_department_filter",
          type: "filter",
          target_section: "filters",
          position: "append",
          filter: {
            field: "hr:department_id",
            label: "Department",
            type: "text",
            permission: "hr:employee:read",
          },
        },
      },
    ]);

    await renderListRenderer({}, permissionWrapperWithPermission(["hr:employee:read"]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    expect(await screen.findByLabelText("Department")).toBeTruthy();
  });

  it("filter: hides an extension filter the current user lacks permission for", async () => {
    useInfiniteListMock.mockReturnValue(oneRow);
    forTargetMock.mockResolvedValue([
      {
        module: "hr",
        moduleDisplayName: "HR",
        loadOrder: 1,
        ref: { extends: "contacts.contacts_list", extension: "hr_department_filter" },
        definition: {
          name: "hr_department_filter",
          type: "filter",
          target_section: "filters",
          position: "append",
          filter: { field: "hr:department_id", label: "Department", type: "text", permission: "hr:employee:read" },
        },
      },
    ]);

    await renderListRenderer({}, permissionWrapperWithPermission([]), "/", {
      ...view,
      columns: [{ field: "name", label: "Name" }],
    });

    await waitFor(() => expect(forTargetMock).toHaveBeenCalled());
    expect(screen.queryByLabelText("Department")).toBeNull();
  });
});
