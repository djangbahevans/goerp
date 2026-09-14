import type { FilterParamValue } from "@goerp/sdk";
import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
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
import type { KanbanViewDeclaration } from "./kanban-manifest-types.js";
import { KanbanRenderer } from "./kanban-renderer.js";

// KanbanRenderer does real data-fetching internally (useInfiniteList) and
// reads route state via TanStack Router in full-page mode — every story
// seeds the exact react-query cache entry useInfiniteList reads, the same
// convention list-renderer.stories.tsx/pivot-renderer.stories.tsx use.
//
// group_label_field/group_color_field have no story here for the same
// reason list-renderer.stories.tsx's row_click doesn't: resolving a
// relation-valued group_by's target resource goes through
// resourceMetadataRegistry -> schemaRegistry.getSchema(), a live GET
// /_meta/schema fetch with no prop-level seam to inject a fake schema.
// That resolve() call fails in Storybook's backend-less environment, and
// KanbanRenderer already degrades gracefully from it (falls back to the
// raw group_by value as the label) — covered by kanban-renderer.test.tsx's
// mocked resourceMetadataRegistry instead.

const MODULE = "crm";

const view: KanbanViewDeclaration = {
  name: "leads_kanban",
  type: "kanban",
  resource: "crm.lead",
  label: "Pipeline",
  group_by: "stage",
  group_values: ["new", "qualified", "won"],
  card_fields: ["avatar_file_id", "display_name", "expected_revenue"],
  quick_create: true,
};

const ROWS: Row[] = [
  {
    id: "lead-1",
    stage: "new",
    display_name: "Acme Corp",
    expected_revenue: "$45,000",
    avatar_file_id: "https://i.pravatar.cc/40?u=acme",
  },
  { id: "lead-2", stage: "new", display_name: "Globex Inc", expected_revenue: "$12,000" },
  { id: "lead-3", stage: "qualified", display_name: "Initech", expected_revenue: "$80,000" },
];

function infiniteListKey(filter: Record<string, FilterParamValue>, cacheKeyPrefix: string | null = null): QueryKey {
  return [
    ...createInfiniteListQueryOptions<Row>("crm.lead", { filter, ...(cacheKeyPrefix ? { cacheKeyPrefix } : {}) })
      .queryKey,
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

function withKanbanProviders(client: QueryClient, initialPath = "/"): Decorator {
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

function defaultClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(infiniteListKey({}), pageOf(ROWS));
  return client;
}

function loadingClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({ queryKey: infiniteListKey({}), queryFn: () => new Promise<never>(() => {}) });
  return client;
}

function errorClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: infiniteListKey({}),
    queryFn: () => Promise.reject(new Error("Couldn't reach the server.")),
  });
  return client;
}

const meta: Meta<typeof KanbanRenderer> = {
  title: "Renderers/KanbanRenderer",
  component: KanbanRenderer,
  args: { view, module: MODULE },
};

export default meta;

type Story = StoryObj<typeof KanbanRenderer>;

export const Default: Story = {
  name: "loaded, grouped into declared group_values columns",
  decorators: [withKanbanProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("new")).toBeInTheDocument());
    await expect(canvas.getByText("qualified")).toBeInTheDocument();
    await expect(canvas.getByText("won")).toBeInTheDocument();
    await expect(canvas.getByText("Acme Corp")).toBeInTheDocument();
    await expect(canvas.getByText("$45,000")).toBeInTheDocument();
    await expect(canvas.getByText("No cards in this column.")).toBeInTheDocument();
  },
};

export const Loading: Story = {
  decorators: [withKanbanProviders(loadingClient(), "/")],
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('[data-skeleton="card"]')).toBeInTheDocument();
  },
};

export const ErrorState: Story = {
  name: "load error, with a working Retry action",
  decorators: [withKanbanProviders(errorClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("Couldn't reach the server.")).toBeInTheDocument();
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};

const { group_values: _groupValues, ...viewWithoutGroupValues } = view;
const emptyView: KanbanViewDeclaration = { ...viewWithoutGroupValues, name: "leads_kanban_empty" };

export const Empty: Story = {
  name: "no rows and no group_values declared",
  args: { view: emptyView },
  decorators: [
    withKanbanProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(infiniteListKey({}), pageOf([]));
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("No pipeline found.")).toBeInTheDocument();
  },
};

export const QuickCreate: Story = {
  name: "quick_create: opens the inline add-card form",
  decorators: [withKanbanProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const addButtons = await waitFor(() => canvas.getAllByRole("button", { name: "+ Add" }));
    await userEvent.click(addButtons[0] as HTMLElement);
    await expect(canvas.getAllByPlaceholderText("Title")[0]).toBeInTheDocument();
  },
};

const RECORD_ID = "cust-500";
const EMBEDDED_FILTER = { customer_id: RECORD_ID };

export const Embedded: Story = {
  name: "embedded mode, locked base filter",
  args: { embedded: true, recordId: RECORD_ID, baseFilter: EMBEDDED_FILTER },
  decorators: [
    withKanbanProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(infiniteListKey(EMBEDDED_FILTER, `embedded:${RECORD_ID}:${view.name}`), pageOf(ROWS));
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Acme Corp")).toBeInTheDocument());
  },
};

const viewWithActions: KanbanViewDeclaration = {
  ...view,
  name: "leads_kanban_with_actions",
  card_actions: [{ label: "Mark Won", type: "route", route: "crm.markWon" }],
  column_actions: [
    { label: "Rename", type: "route", route: "crm.renameStage" },
    { label: "Archive", type: "route", route: "crm.archiveStage" },
  ],
};

function permissionValue() {
  return createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() });
}

export const WithActions: Story = {
  name: "card_actions/column_actions, reusing list-actions.tsx's Action handling",
  args: { view: viewWithActions },
  decorators: [
    withKanbanProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(infiniteListKey({}), pageOf(ROWS));
        return client;
      })(),
      "/",
    ),
    (Story) => (
      <PermissionContext.Provider value={permissionValue()}>
        <Story />
      </PermissionContext.Provider>
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Mark Won")).toBeInTheDocument());
    await expect(canvas.getAllByRole("button", { name: "Column actions" })[0]).toBeInTheDocument();
  },
};
