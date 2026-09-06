import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { groupRows, ListRenderer } from "./list-renderer.js";
import type { ListViewDeclaration } from "./list-view-types.js";

const { useInfiniteListMock, useRelationLabelsMock } = vi.hoisted(() => ({
  useInfiniteListMock: vi.fn(),
  useRelationLabelsMock: vi.fn(() => new Map()),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock, useRelationLabels: useRelationLabelsMock };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
  useRelationLabelsMock.mockClear();
  useRelationLabelsMock.mockImplementation(() => new Map());
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
}

const fullAccess = permissionWrapper({
  "contacts.contact": { name: { read: true, write: true }, ssn: { read: true, write: true } },
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

    expect(screen.getByRole("status").textContent).toContain("Loading");
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

    expect(screen.getByRole("status").textContent).toContain("No contacts found");
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

    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("state");
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
      { key: "customer_id", resource: "sales.customer", labelField: "name", ids: ["c1"] },
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
      { key: "customer_id", resource: "sales.customer", labelField: "name", ids: ["c1"] },
      { key: "customer_id", resource: "sales.customer", labelField: "name", ids: ["c2"] },
    ]);
  });
});
