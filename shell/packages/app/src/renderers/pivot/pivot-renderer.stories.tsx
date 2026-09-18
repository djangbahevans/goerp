import type { FilterParamValue } from "@goerp/sdk";
import { createPivotDataQueryOptions, type PivotResponse } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import type { QueryKey } from "@tanstack/react-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, waitFor, within } from "storybook/test";
import type { PivotViewDeclaration } from "./pivot-manifest-types.js";
import { PivotRenderer } from "./pivot-renderer.js";

// PivotRenderer does real data-fetching internally (usePivotData) and
// reads route state via TanStack Router (useListState's full-page half
// runs unconditionally even in embedded mode) — every story seeds the
// exact react-query cache entry usePivotData reads, the same convention
// list-renderer.stories.tsx uses, rather than mocking @goerp/sdk (not
// possible in a real browser the way pivot-renderer.test.tsx's vi.mock is).
// use_wasm: false pins every story below to that server-aggregation path
// — the use_wasm: true (default) path fetches real Parquet bytes and runs
// DuckDB-WASM in a Web Worker, neither of which this file's react-query-
// cache-seeding approach can stand in for; it has no Storybook coverage.

const MODULE = "sales";

const view: PivotViewDeclaration = {
  name: "sales_pivot",
  type: "pivot",
  resource: "sales.order",
  label: "Sales Analysis",
  rows: ["customer_name"],
  columns: ["state"],
  values: [
    { field: "amount_total", aggregation: "sum", label: "Revenue", format: "currency" },
    { field: "id", aggregation: "count", label: "Order Count" },
  ],
  allow_download: true,
  use_wasm: false,
};

const RESPONSE: PivotResponse = {
  cells: [
    { row: ["Acme Corp"], column: ["confirmed"], values: { amount_total_sum: 450050, id_count: 3 } },
    { row: ["Acme Corp"], column: ["done"], values: { amount_total_sum: 120000, id_count: 1 } },
    { row: ["Acme Corp"], column: [null], values: { amount_total_sum: 570050, id_count: 4 } },
    { row: ["Globex Inc"], column: ["confirmed"], values: { amount_total_sum: 89000, id_count: 2 } },
    { row: ["Globex Inc"], column: [null], values: { amount_total_sum: 89000, id_count: 2 } },
  ],
};

function pivotDataKey(
  filter: Record<string, FilterParamValue> = {},
  cacheKeyPrefix: string | null = null,
  viewOverride: PivotViewDeclaration = view,
): QueryKey {
  return [
    ...createPivotDataQueryOptions("sales.order", {
      rows: viewOverride.rows,
      columns: viewOverride.columns,
      values: viewOverride.values,
      filter,
      ...(cacheKeyPrefix !== null ? { cacheKeyPrefix } : {}),
    }).queryKey,
  ];
}

// goerp#794's known limitation: a manifest declaring zero fields on an
// axis ("total revenue by customer, no column breakdown") used to render
// as PivotGrid's empty state, since an empty rowHeaders/columnHeaders
// array reads as "no data" there — mapPivotResponse now synthesizes a
// single implicit Total node for a declared-empty axis instead.
const rowsOnlyView: PivotViewDeclaration = {
  ...view,
  name: "sales_pivot_rows_only",
  columns: [],
};

const ROWS_ONLY_RESPONSE: PivotResponse = {
  cells: [
    { row: ["Acme Corp"], column: [], values: { amount_total_sum: 570050, id_count: 4 } },
    { row: ["Globex Inc"], column: [], values: { amount_total_sum: 89000, id_count: 2 } },
  ],
};

function seededClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, retryOnMount: false, staleTime: Number.POSITIVE_INFINITY } },
  });
}

function withPivotProviders(client: QueryClient, initialPath = "/"): Decorator {
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
  client.setQueryData(pivotDataKey(), RESPONSE);
  return client;
}

function loadingClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({ queryKey: pivotDataKey(), queryFn: () => new Promise<never>(() => {}) });
  return client;
}

function errorClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: pivotDataKey(),
    queryFn: () => Promise.reject(new Error("Couldn't reach the server.")),
  });
  return client;
}

function emptyClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(pivotDataKey(), { cells: [] } satisfies PivotResponse);
  return client;
}

const meta: Meta<typeof PivotRenderer> = {
  title: "Renderers/PivotRenderer",
  component: PivotRenderer,
  args: { view, module: MODULE },
};

export default meta;

type Story = StoryObj<typeof PivotRenderer>;

export const Default: Story = {
  name: "loaded, with row/column subtotals and a download button",
  decorators: [withPivotProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table"));
    const body = within(table);

    await expect(canvas.getByRole("heading", { name: "Sales Analysis" })).toBeInTheDocument();
    await expect(body.getByText("Acme Corp")).toBeInTheDocument();
    await expect(body.getByText("Globex Inc")).toBeInTheDocument();
    await expect(body.getByText("confirmed")).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Download" })).toBeInTheDocument();
  },
};

export const Loading: Story = {
  decorators: [withPivotProviders(loadingClient(), "/")],
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('[data-skeleton="table"]')).toBeInTheDocument();
  },
};

export const ErrorState: Story = {
  name: "load error, with a working Retry action",
  decorators: [withPivotProviders(errorClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("Couldn't reach the server.")).toBeInTheDocument();
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};

export const Empty: Story = {
  decorators: [withPivotProviders(emptyClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("No sales analysis data found.")).toBeInTheDocument();
  },
};

export const RowsOnly: Story = {
  name: "no column breakdown declared — renders under an implicit Total column",
  args: { view: rowsOnlyView },
  decorators: [
    withPivotProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(pivotDataKey({}, null, rowsOnlyView), ROWS_ONLY_RESPONSE);
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table"));
    const body = within(table);

    await expect(body.getByText("Acme Corp")).toBeInTheDocument();
    await expect(body.getByText("Globex Inc")).toBeInTheDocument();
    await expect(canvas.getByText("Total")).toBeInTheDocument();
  },
};

export const Embedded: Story = {
  name: "embedded mode, locked base filter",
  args: { embedded: true, recordId: "cust-1", baseFilter: { customer_name: "Acme Corp" } },
  decorators: [
    withPivotProviders(
      (() => {
        const client = seededClient();
        client.setQueryData(pivotDataKey({ customer_name: "Acme Corp" }, "embedded:cust-1:sales_pivot"), RESPONSE);
        return client;
      })(),
      "/",
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table"));
    await expect(within(table).getByText("Acme Corp")).toBeInTheDocument();
  },
};
