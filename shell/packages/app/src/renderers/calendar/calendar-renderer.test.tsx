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
import type { CalendarViewDeclaration } from "./calendar-manifest-types.js";
import { CalendarRenderer } from "./calendar-renderer.js";

const { useInfiniteListMock, resolveViewPathMock } = vi.hoisted(() => ({
  useInfiniteListMock: vi.fn(),
  resolveViewPathMock: vi.fn(),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewPathRegistry: { resolve: resolveViewPathMock } };
});

afterEach(() => {
  cleanup();
  useInfiniteListMock.mockReset();
  resolveViewPathMock.mockReset();
});

const INITIAL_DATE = new Date(2026, 4, 13); // Wednesday, May 13 2026

const view: CalendarViewDeclaration = {
  name: "activities_calendar",
  type: "calendar",
  resource: "contacts.activity",
  label: "Activity Calendar",
  date_field: "scheduled_at",
  title_field: "subject",
  on_click: "activity_form",
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

async function renderCalendarRenderer(
  props: { embedded?: boolean; baseFilter?: Record<string, string>; recordId?: string } = {},
  viewOverride: CalendarViewDeclaration = view,
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
          <CalendarRenderer view={viewOverride} module="contacts" initialDate={INITIAL_DATE} {...props} />
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

describe("CalendarRenderer", () => {
  beforeEach(() => {
    resolveViewPathMock.mockResolvedValue("/activities/{id}");
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

    await renderCalendarRenderer();

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

    await renderCalendarRenderer();

    expect(screen.getByRole("alert").textContent).toContain("boom");
    fireEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalled();
  });

  it("maps rows into calendar events using date_field/title_field", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "act-1", scheduled_at: "2026-05-13T09:00:00Z", subject: "Call Acme" }]),
    );

    await renderCalendarRenderer();

    expect(await screen.findByText("Call Acme")).toBeTruthy();
  });

  it("fetches with a date_field range matching the initial (month) view", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer();

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.activity",
      expect.objectContaining({
        filter: expect.objectContaining({ scheduled_at: { gte: "2026-04-26", lte: "2026-06-06" } }),
      }),
    );
  });

  it("refetches with a narrower range after switching to the day view", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer();
    fireEvent.click(screen.getByRole("tab", { name: "Day" }));

    await waitFor(() =>
      expect(useInfiniteListMock).toHaveBeenLastCalledWith(
        "contacts.activity",
        expect.objectContaining({
          filter: expect.objectContaining({ scheduled_at: { gte: "2026-05-13", lte: "2026-05-13" } }),
        }),
      ),
    );
  });

  it("navigates to on_click's resolved path with the event's record id when an event is clicked", async () => {
    useInfiniteListMock.mockReturnValue(
      pagedResult([{ id: "act-1", scheduled_at: "2026-05-13T09:00:00Z", subject: "Call Acme" }]),
    );

    await renderCalendarRenderer();
    fireEvent.click(await screen.findByText("Call Acme"));

    await waitFor(() => expect(screen.getByRole("tab", { name: "Month" })).toBeTruthy());
    expect(resolveViewPathMock).toHaveBeenCalledWith("activity_form", "contacts");
  });

  it("quick_create: confirming an empty date's popover navigates to on_date_click's resolved path with the clicked date", async () => {
    resolveViewPathMock.mockResolvedValue("/activities/new");
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    const router = await renderCalendarRenderer(
      {},
      { ...view, quick_create: true, on_date_click: "activity_quick_create_form" },
    );
    fireEvent.click(await screen.findByRole("button", { name: /May 14, 2026/ }));
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(resolveViewPathMock).toHaveBeenCalledWith("activity_quick_create_form", "contacts"));
    await waitFor(() => expect(router.state.location.href).toContain("scheduled_at=2026-05-14"));
  });

  it("quick_create: shows no date-click affordance at all when on_date_click isn't declared", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer({}, { ...view, quick_create: true });
    fireEvent.click(await screen.findByRole("button", { name: /May 14, 2026/ }));

    expect(screen.queryByRole("dialog", { name: "Quick create" })).toBeNull();
  });

  it("quick_create: reports an error instead of navigating when on_date_click doesn't resolve", async () => {
    const { toast } = await import("@goerp/sdk/notifications");
    const toastSpy = vi.spyOn(toast, "error").mockImplementation(() => {});
    resolveViewPathMock.mockResolvedValue(null);
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer({}, { ...view, quick_create: true, on_date_click: "activity_quick_create_form" });
    fireEvent.click(await screen.findByRole("button", { name: /May 14, 2026/ }));
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(toastSpy).toHaveBeenCalled());
  });

  it("applies default_filters once on mount", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer({ embedded: true }, { ...view, default_filters: { assigned_to: "u1" } });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.activity",
      expect.objectContaining({ filter: expect.objectContaining({ assigned_to: "u1" }) }),
    );
  });

  it("merges the embedded base filter and isolates the cache key by view and record", async () => {
    useInfiniteListMock.mockReturnValue(pagedResult([]));

    await renderCalendarRenderer({ embedded: true, baseFilter: { contact_id: "01j" }, recordId: "01j" });

    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.activity",
      expect.objectContaining({
        filter: expect.objectContaining({ contact_id: "01j" }),
        cacheKeyPrefix: "embedded:01j:activities_calendar",
      }),
    );
  });
});
