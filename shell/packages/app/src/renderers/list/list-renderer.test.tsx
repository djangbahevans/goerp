import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
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
import {
  computeDefaultFilters,
  groupRows,
  ListRenderer,
  nextSortValue,
  rowClickHref,
  sortDirectionOf,
} from "./list-renderer.js";
import type { ListViewDeclaration } from "./list-view-types.js";

const { useInfiniteListMock, useRelationLabelsMock, resolveViewPathMock } = vi.hoisted(() => ({
  useInfiniteListMock: vi.fn(),
  useRelationLabelsMock: vi.fn(() => new Map()),
  resolveViewPathMock: vi.fn(async (): Promise<string | null> => "/contacts/{id}"),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock, useRelationLabels: useRelationLabelsMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewPathRegistry: { resolve: resolveViewPathMock } };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
  useRelationLabelsMock.mockClear();
  useRelationLabelsMock.mockImplementation(() => new Map());
  resolveViewPathMock.mockClear();
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
  props: { embedded?: boolean; baseFilter?: Record<string, string>; recordId?: string; module?: string },
  Wrapper: ({ children }: { children: ReactNode }) => React.JSX.Element,
  initialPath = "/",
  viewOverride: ListViewDeclaration = view,
) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <Wrapper>
        <ListRenderer view={viewOverride} module="contacts" {...props} />
      </Wrapper>
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

describe("computeDefaultFilters", () => {
  it("coerces default_filters' plain values, arrays becoming string arrays", () => {
    const defaults = computeDefaultFilters({
      ...view,
      default_filters: { is_active: true, count: 3, tag_ids: [1, 2] },
    });
    expect(defaults).toEqual({ is_active: true, count: 3, tag_ids: ["1", "2"] });
  });

  it("wraps a text filter's default in {like}", () => {
    const defaults = computeDefaultFilters({
      ...view,
      filters: [{ field: "name", label: "Name", type: "text", default: "acme" }],
    });
    expect(defaults).toEqual({ name: { like: "acme" } });
  });

  it("passes a daterange/number_range filter's default through as {gte,lte}", () => {
    const defaults = computeDefaultFilters({
      ...view,
      filters: [{ field: "created_at", label: "Created", type: "daterange", default: { gte: "2026-01-01" } }],
    });
    expect(defaults).toEqual({ created_at: { gte: "2026-01-01" } });
  });

  it("stringifies a number_range filter's numeric default bounds", () => {
    const defaults = computeDefaultFilters({
      ...view,
      filters: [{ field: "amount", label: "Amount", type: "number_range", default: { gte: 10, lte: 100 } }],
    });
    expect(defaults).toEqual({ amount: { gte: "10", lte: "100" } });
  });

  it("accepts a range-shaped default_filters value even with no per-field type context", () => {
    const defaults = computeDefaultFilters({ ...view, default_filters: { created_at: { gte: "2026-01-01" } } });
    expect(defaults).toEqual({ created_at: { gte: "2026-01-01" } });
  });

  it("coerces a multi_select/tags filter's array default to a string array", () => {
    const defaults = computeDefaultFilters({
      ...view,
      filters: [{ field: "tag_ids", label: "Tags", type: "tags", default: ["a", "b"] }],
    });
    expect(defaults).toEqual({ tag_ids: ["a", "b"] });
  });

  it("default_filters wins over a filter's own default for the same field", () => {
    const defaults = computeDefaultFilters({
      ...view,
      default_filters: { type: "company" },
      filters: [{ field: "type", label: "Type", type: "select", default: "person" }],
    });
    expect(defaults).toEqual({ type: "company" });
  });

  it("returns an empty object when neither default_filters nor any filter declares a default", () => {
    expect(computeDefaultFilters({ ...view, filters: [{ field: "type", label: "Type", type: "select" }] })).toEqual({});
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

    fireEvent.click(rowCheckboxes[0] as HTMLInputElement);
    expect(screen.getByText("1 selected")).toBeTruthy();

    fireEvent.click(screen.getByRole("checkbox", { name: /Select all/ }));
    expect(screen.getByText("2 selected")).toBeTruthy();

    fireEvent.click(screen.getByRole("checkbox", { name: /Select all/ }));
    expect(screen.queryByText(/selected/)).toBeNull();
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

  it("Load more: disabled with ActionButton's own loading treatment while isFetchingNextPage", async () => {
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

    expect(screen.getByRole("button", { name: "Load more" }).hasAttribute("disabled")).toBe(true);
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
