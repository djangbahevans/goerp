import type { FilterParamValue } from "@goerp/sdk";
import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import type { InfiniteData, QueryKey } from "@tanstack/react-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import type { Row } from "../list/list-view-types.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import { TimelineRenderer } from "./timeline-renderer.js";

// TimelineRenderer does real data-fetching internally (useInfiniteList) —
// every story seeds the exact react-query cache entry it reads, the same
// convention calendar-renderer.stories.tsx/kanban-renderer.stories.tsx use.

const MODULE = "project";
const INITIAL_DATE = new Date(2026, 4, 13); // Wednesday, May 13 2026

const view: TimelineViewDeclaration = {
  name: "project_timeline",
  type: "timeline",
  resource: "project.task",
  label: "Project Timeline",
  start_field: "planned_start",
  end_field: "planned_end",
  group_by: "assignee_id",
  label_field: "name",
  color_field: "priority",
  color_map: { high: "#EF4444", medium: "#F59E0B", low: "#10B981" },
  allow_drag: true,
  allow_resize: true,
  default_range: "month",
};

const ROWS: Row[] = [
  {
    id: "task-1",
    name: "Design review",
    planned_start: "2026-05-05",
    planned_end: "2026-05-09",
    assignee_id: "Alice",
    priority: "high",
  },
  // Overlaps task-1 within the same group — exercises sub-lane stacking.
  {
    id: "task-2",
    name: "Spec doc",
    planned_start: "2026-05-08",
    planned_end: "2026-05-12",
    assignee_id: "Alice",
    priority: "medium",
  },
  // Starts before the visible month and is still running through it — only
  // shows up because the fetch is an overlap query, not a start-in-range one.
  {
    id: "task-3",
    name: "Backend API",
    planned_start: "2026-04-20",
    planned_end: "2026-05-15",
    assignee_id: "Bob",
    priority: "high",
  },
  {
    id: "task-4",
    name: "Frontend polish",
    planned_start: "2026-05-20",
    planned_end: "2026-05-25",
    assignee_id: "Bob",
    priority: "low",
  },
];

function infiniteListKey(filter: Record<string, FilterParamValue>, cacheKeyPrefix: string | null = null): QueryKey {
  return [
    ...createInfiniteListQueryOptions<Row>("project.task", {
      filter,
      ...(cacheKeyPrefix ? { cacheKeyPrefix } : {}),
    }).queryKey,
  ];
}

function pageOf(rows: Row[]): InfiniteData<{ data: Row[]; meta: { cursor: null; hasMore: false } }> {
  return { pages: [{ data: rows, meta: { cursor: null, hasMore: false } }], pageParams: [undefined] };
}

function seededClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, retryOnMount: false, staleTime: Number.POSITIVE_INFINITY } },
  });
}

function withTimelineProviders(client: QueryClient, initialPath = "/"): Decorator {
  return (Story) => {
    const rootRoute = createRootRoute();
    const indexRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: () => (
        <QueryClientProvider client={client}>
          <Story />
        </QueryClientProvider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([indexRoute]),
      history: createMemoryHistory({ initialEntries: [initialPath] }),
    });
    return <RouterProvider router={router} />;
  };
}

// Matches timeline-date-utils.ts's visibleRange for the default month view
// at INITIAL_DATE — the real calendar month, not a padded grid — so the
// seeded cache entry is the one TimelineRenderer's overlap-query fetch
// actually reads.
const DEFAULT_RANGE_FILTER = { planned_start: { lte: "2026-05-31" }, planned_end: { gte: "2026-05-01" } };

function defaultClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(infiniteListKey(DEFAULT_RANGE_FILTER), pageOf(ROWS));
  return client;
}

function loadingClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: infiniteListKey(DEFAULT_RANGE_FILTER),
    queryFn: () => new Promise<never>(() => {}),
  });
  return client;
}

function errorClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: infiniteListKey(DEFAULT_RANGE_FILTER),
    queryFn: () => Promise.reject(new Error("Couldn't reach the server.")),
  });
  return client;
}

const meta: Meta<typeof TimelineRenderer> = {
  title: "Renderers/TimelineRenderer",
  component: TimelineRenderer,
  args: { view, module: MODULE, initialDate: INITIAL_DATE },
};

export default meta;

type Story = StoryObj<typeof TimelineRenderer>;

export const Default: Story = {
  name: "loaded, month view with grouped rows and stacked lanes",
  decorators: [withTimelineProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("group", { name: /Design review/ })).toBeInTheDocument());
    await expect(canvas.getByRole("group", { name: /Backend API/ })).toBeInTheDocument();
    await expect(canvas.getByText("Alice")).toBeInTheDocument();
    await expect(canvas.getByText("Bob")).toBeInTheDocument();
    await expect(canvas.getByRole("tab", { name: "Month" })).toBeInTheDocument();
    await expect(canvas.getByRole("heading", { level: 1, name: "Project Timeline" })).toBeInTheDocument();
  },
};

export const Loading: Story = {
  decorators: [withTimelineProviders(loadingClient(), "/")],
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('[data-skeleton="card"]')).toBeInTheDocument();
  },
};

export const ErrorState: Story = {
  name: "load error, with a working Retry action",
  decorators: [withTimelineProviders(errorClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("Couldn't reach the server.")).toBeInTheDocument();
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};

export const WeekView: Story = {
  name: "switched to week view, re-fetching a narrower range",
  decorators: [
    withTimelineProviders(
      (() => {
        const client = defaultClient();
        client.setQueryData(
          infiniteListKey({ planned_start: { lte: "2026-05-16" }, planned_end: { gte: "2026-05-10" } }),
          pageOf(ROWS),
        );
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("group", { name: /Design review/ })).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("tab", { name: "Week" }));
    await expect(canvas.getByRole("tab", { name: "Week", selected: true })).toBeInTheDocument();
  },
};

const RECORD_ID = "project-500";
const EMBEDDED_FILTER = { project_id: RECORD_ID };

export const Embedded: Story = {
  name: "embedded mode, locked base filter",
  args: { embedded: true, recordId: RECORD_ID, baseFilter: EMBEDDED_FILTER },
  decorators: [
    withTimelineProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(
          infiniteListKey({ ...DEFAULT_RANGE_FILTER, ...EMBEDDED_FILTER }, `embedded:${RECORD_ID}:${view.name}`),
          pageOf(ROWS),
        );
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("group", { name: /Design review/ })).toBeInTheDocument());
    await expect(canvas.queryByRole("heading", { name: "Project Timeline" })).not.toBeInTheDocument();
  },
};
