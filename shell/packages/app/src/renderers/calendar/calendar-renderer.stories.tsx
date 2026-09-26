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
import type { CalendarViewDeclaration } from "./calendar-manifest-types.js";
import { CalendarRenderer } from "./calendar-renderer.js";

// CalendarRenderer does real data-fetching internally (useInfiniteList) and
// reads route state via TanStack Router in full-page mode — every story
// seeds the exact react-query cache entry useInfiniteList reads, the same
// convention list-renderer.stories.tsx/kanban-renderer.stories.tsx use.
//
// on_click/on_date_click have no story exercising the actual navigation for
// the same reason list-renderer.stories.tsx's row_click doesn't: resolving
// either goes through the live viewPathRegistry -> schemaRegistry ->
// GET /_meta/schema fetch, which fails in Storybook's backend-less
// environment — CalendarRenderer already degrades from it (no navigation,
// or a toast for quick_create) — covered by calendar-renderer.test.tsx's
// mocked viewPathRegistry instead.

const MODULE = "contacts";
const INITIAL_DATE = new Date(2026, 4, 13); // Wednesday, May 13 2026

const view: CalendarViewDeclaration = {
  name: "activities_calendar",
  type: "calendar",
  resource: "contacts.activity",
  label: "Activity Calendar",
  date_field: "scheduled_at",
  title_field: "subject",
  color_field: "activity_type",
  color_map: { call: "#3B82F6", meeting: "#8B5CF6", email: "#10B981" },
  quick_create: true,
};

const ROWS: Row[] = [
  { id: "act-1", scheduled_at: "2026-05-13T09:00:00", subject: "Call Acme Corp", activity_type: "call" },
  { id: "act-2", scheduled_at: "2026-05-13T14:00:00", subject: "Demo with Globex", activity_type: "meeting" },
  { id: "act-3", scheduled_at: "2026-05-15T10:00:00", subject: "Follow-up email", activity_type: "email" },
];

function infiniteListKey(filter: Record<string, FilterParamValue>, cacheKeyPrefix: string | null = null): QueryKey {
  return [
    ...createInfiniteListQueryOptions<Row>("contacts.activity", {
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

function withCalendarProviders(client: QueryClient, initialPath = "/"): Decorator {
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

// Matches calendar-events.ts's initialViewMode/calendar-date-utils.ts's
// visibleRange for the default month view at INITIAL_DATE, so the seeded
// cache entry is the one CalendarRenderer's first render actually reads.
const DEFAULT_RANGE_FILTER = { scheduled_at: { gte: "2026-04-26", lte: "2026-06-06" } };

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

const meta: Meta<typeof CalendarRenderer> = {
  title: "Renderers/CalendarRenderer",
  component: CalendarRenderer,
  args: { view, module: MODULE, initialDate: INITIAL_DATE },
};

export default meta;

type Story = StoryObj<typeof CalendarRenderer>;

export const Default: Story = {
  name: "loaded, month view with color-mapped events",
  decorators: [withCalendarProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Call Acme Corp")).toBeInTheDocument());
    await expect(canvas.getByText("Demo with Globex")).toBeInTheDocument();
    await expect(canvas.getByRole("tab", { name: "Month" })).toBeInTheDocument();
    await expect(canvas.getByRole("heading", { level: 1, name: "Activity Calendar" })).toBeInTheDocument();
  },
};

export const Loading: Story = {
  decorators: [withCalendarProviders(loadingClient(), "/")],
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('[data-skeleton="card"]')).toBeInTheDocument();
  },
};

export const ErrorState: Story = {
  name: "load error, with a working Retry action",
  decorators: [withCalendarProviders(errorClient(), "/")],
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
    withCalendarProviders(
      (() => {
        const client = defaultClient();
        client.setQueryData(infiniteListKey({ scheduled_at: { gte: "2026-05-10", lte: "2026-05-16" } }), pageOf(ROWS));
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Call Acme Corp")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("tab", { name: "Week" }));
    await expect(canvas.getByRole("table", { name: "Schedule" })).toBeInTheDocument();
  },
};

export const QuickCreate: Story = {
  name: "quick_create: opens the confirm popover on an empty date",
  // Quick create needs a target: CalendarRenderer enables it only when
  // on_date_click is declared too.
  args: { view: { ...view, on_date_click: "activity_form" } },
  decorators: [withCalendarProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Call Acme Corp")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("button", { name: /May 20, 2026/ }));
    await expect(canvas.getByRole("dialog", { name: "Quick create" })).toBeInTheDocument();
  },
};

const RECORD_ID = "contact-500";
const EMBEDDED_FILTER = { contact_id: RECORD_ID };

export const Embedded: Story = {
  name: "embedded mode, locked base filter",
  args: { embedded: true, recordId: RECORD_ID, baseFilter: EMBEDDED_FILTER },
  decorators: [
    withCalendarProviders(
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
    await waitFor(() => expect(canvas.getByText("Call Acme Corp")).toBeInTheDocument());
    await expect(canvas.queryByRole("heading", { name: "Activity Calendar" })).not.toBeInTheDocument();
  },
};
