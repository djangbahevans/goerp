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
import { ListRenderer } from "./list-renderer.js";
import type { ListViewDeclaration } from "./list-view-types.js";

const { useInfiniteListMock } = vi.hoisted(() => ({ useInfiniteListMock: vi.fn() }));
vi.mock("@goerp/sdk/react", () => ({ useInfiniteList: useInfiniteListMock }));

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
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
  props: { embedded?: boolean; baseFilter?: Record<string, string> },
  Wrapper: ({ children }: { children: ReactNode }) => React.JSX.Element,
  initialPath = "/",
) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <Wrapper>
        <ListRenderer view={view} {...props} />
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

describe("ListRenderer", () => {
  it("shows a loading state while the initial page is in flight", async () => {
    useInfiniteListMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      isFetching: true,
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
      isFetching: false,
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
      isFetching: false,
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
      isFetching: false,
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
      isFetching: false,
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

  it("embedded mode: merges baseFilter with local state and defaults sort from the view, ignoring the URL", async () => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      isLoading: false,
      isError: false,
      isFetching: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
      error: null,
    });

    await renderListRenderer(
      { embedded: true, baseFilter: { parent_id: "42" } },
      fullAccess,
      "/?filter[is_active]=true",
    );

    expect(useInfiniteListMock).toHaveBeenCalledWith("contacts.contact", {
      filter: { parent_id: "42" },
      sort: "name",
    });
  });
});
