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
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import { TimelineRenderer } from "./timeline-renderer.js";

const { useInfiniteListMock } = vi.hoisted(() => ({ useInfiniteListMock: vi.fn() }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
});

const INITIAL_DATE = new Date(2026, 4, 13); // Wednesday, May 13 2026

const view: TimelineViewDeclaration = {
  name: "project_timeline",
  type: "timeline",
  resource: "project.task",
  label: "Timeline",
  start_field: "planned_start",
  end_field: "planned_end",
  label_field: "name",
};

function pagedResult(rows: Row[]) {
  return {
    data: { pages: [{ data: rows, meta: { cursor: null, hasMore: false } }] },
    fetchNextPage: vi.fn(),
    hasNextPage: false,
    isFetchingNextPage: false,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  };
}

async function renderTimelineRenderer(
  props: { embedded?: boolean; baseFilter?: Record<string, string>; recordId?: string } = {},
  viewOverride: TimelineViewDeclaration = view,
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
          <TimelineRenderer view={viewOverride} module="project" initialDate={INITIAL_DATE} {...props} />
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
  return router;
}

describe("TimelineRenderer", () => {
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

    await renderTimelineRenderer();

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

    await renderTimelineRenderer();

    expect(screen.getByRole("alert").textContent).toContain("boom");
    fireEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalled();
  });

  it("maps rows into timeline bars using start_field/end_field/label_field", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "task-1", planned_start: "2026-05-10", planned_end: "2026-05-14", name: "Design review" }]),
    );

    await renderTimelineRenderer();

    expect(await screen.findByRole("group", { name: /Design review/ })).toBeTruthy();
  });

  it("fetches with an overlap filter matching the initial (month) range", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderTimelineRenderer();

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "project.task",
      expect.objectContaining({
        filter: expect.objectContaining({
          planned_start: { lte: "2026-05-31" },
          planned_end: { gte: "2026-05-01" },
        }),
      }),
    );
  });

  it("applies default_filters once on mount", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderTimelineRenderer({ embedded: true }, { ...view, default_filters: { assignee_id: "u1" } });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "project.task",
      expect.objectContaining({ filter: expect.objectContaining({ assignee_id: "u1" }) }),
    );
  });

  it("merges the embedded base filter and isolates the cache key by view and record", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderTimelineRenderer({ embedded: true, baseFilter: { project_id: "01j" }, recordId: "01j" });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "project.task",
      expect.objectContaining({
        filter: expect.objectContaining({ project_id: "01j" }),
        cacheKeyPrefix: "embedded:01j:project_timeline",
      }),
    );
  });
});
