import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
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
import { ListRenderer } from "./list-renderer.js";
import type { ListColumn, ListViewDeclaration, Row } from "./list-view-types.js";

// shell-architecture.md §20's ListRenderer does real data-fetching
// internally (useInfiniteList/useRelationLabels) and reads route state via
// TanStack Router in full-page mode — every story seeds the exact
// react-query cache entries those hooks read, the same way
// notification-sheet.stories.tsx does, rather than mocking @goerp/sdk (not
// possible in a real browser the way list-renderer.test.tsx's vi.mock is).

const MODULE = "sales";

// view-system.md §4's full column-types reference table (list-view-types.ts'
// ColumnType), on one manifest — goerp#575's own AC for this reference.
const COLUMNS: ListColumn[] = [
  { field: "reference", label: "Reference", type: "text", primary: true, sortable: true },
  { field: "amount_total", label: "Amount", type: "currency", currency_field: "currency_code", align: "right" },
  { field: "quantity", label: "Qty", type: "number", align: "right" },
  { field: "discount_pct", label: "Discount", type: "percent" },
  { field: "order_date", label: "Order Date", type: "date" },
  { field: "confirmed_at", label: "Confirmed", type: "datetime" },
  { field: "pickup_time", label: "Pickup", type: "time" },
  { field: "updated_at", label: "Updated", type: "relative_time" },
  { field: "is_active", label: "Active", type: "boolean" },
  {
    field: "state",
    label: "Status",
    type: "badge",
    badge_config: {
      draft: { label: "Draft", color: "gray" },
      confirmed: { label: "Confirmed", color: "blue" },
      done: { label: "Done", color: "green" },
      cancelled: { label: "Cancelled", color: "red" },
    },
  },
  { field: "sales_rep", label: "Sales Rep", type: "avatar", avatar_field: "sales_rep_avatar_url" },
  { field: "email", label: "Email", type: "email" },
  { field: "phone", label: "Phone", type: "phone" },
  { field: "tracking_url", label: "Tracking", type: "url" },
  { field: "ship_country", label: "Ship Country", type: "country" },
  { field: "tags", label: "Tags", type: "tags" },
  {
    field: "customer_id",
    label: "Customer",
    type: "relation",
    resource: "sales.customer",
    resource_label_field: "name",
  },
  { field: "invoice_file", label: "Invoice", type: "file" },
  { field: "label_color", label: "Color", type: "color" },
  { field: "metadata", label: "Metadata", type: "json" },
  { field: "internal_note", label: "Note", type: "custom" },
];

const view: ListViewDeclaration = {
  name: "orders_list",
  type: "list",
  resource: "sales.order",
  label: "Orders",
  columns: COLUMNS,
  filters: [{ field: "is_active", label: "Active", type: "boolean" }],
  group_by_options: ["state"],
  bulk_actions: [{ label: "Export Selected", type: "export", route: "sales.exportOrders" }],
  actions: [{ label: "New Order", type: "create", view: "sales_order_form", style: "primary", icon: "plus" }],
};

const ROWS: Row[] = [
  {
    id: "order-1",
    reference: "SO-1042",
    amount_total: 450.5,
    currency_code: "USD",
    quantity: 12,
    discount_pct: 0.15,
    order_date: "2026-08-01",
    confirmed_at: "2026-08-02T10:30:00Z",
    pickup_time: "2026-08-05T14:00:00Z",
    updated_at: new Date(Date.now() - 3_600_000).toISOString(),
    is_active: true,
    state: "confirmed",
    sales_rep: "Jane Doe",
    sales_rep_avatar_url: null,
    email: "buyer@acme.example",
    phone: "+15551234567",
    tracking_url: "https://track.example.com/SO-1042",
    ship_country: "US",
    tags: ["rush", "wholesale"],
    customer_id: "c1",
    invoice_file: { url: "https://files.example.com/inv-1042.pdf", name: "invoice-1042.pdf" },
    label_color: "#3B82F6",
    metadata: { source: "web", campaign: "summer-sale" },
    internal_note: "Called customer to confirm address.",
  },
  {
    id: "order-2",
    reference: "SO-1043",
    amount_total: 120,
    currency_code: "USD",
    quantity: 3,
    discount_pct: 0,
    order_date: "2026-08-03",
    confirmed_at: "2026-08-03T09:00:00Z",
    pickup_time: "2026-08-06T09:00:00Z",
    updated_at: new Date(Date.now() - 86_400_000).toISOString(),
    is_active: false,
    state: "done",
    sales_rep: "Sam Lee",
    sales_rep_avatar_url: null,
    email: "buyer2@globex.example",
    phone: "+15557654321",
    tracking_url: "https://track.example.com/SO-1043",
    ship_country: "DE",
    tags: ["retail"],
    customer_id: "c2",
    invoice_file: { url: "https://files.example.com/inv-1043.pdf", name: "invoice-1043.pdf" },
    label_color: "#10B981",
    metadata: { source: "pos" },
    internal_note: "",
  },
];

const RELATION_LABELS_KEY: QueryKey = [
  "relation-labels",
  "sales.customer",
  "name",
  `${MODULE}.${view.name}.customer_id`,
  ["c1", "c2"],
];

function infiniteListKey(filter: Record<string, unknown>, cacheKeyPrefix: string | null = null): QueryKey {
  return ["infinite-list", cacheKeyPrefix, "sales.order", filter, null, null];
}

function pageOf(rows: Row[]): InfiniteData<{ data: Row[]; meta: { cursor: null; hasMore: false } }> {
  return { pages: [{ data: rows, meta: { cursor: null, hasMore: false } }], pageParams: [undefined] };
}

function seededClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } } });
}

// Wraps in both QueryClientProvider (react-query cache the hooks read) and
// RouterProvider (useListState's full-page half runs unconditionally even
// in embedded mode — use-list-state.ts).
function withListProviders(client: QueryClient, initialPath = "/"): Decorator {
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
  client.setQueryData(RELATION_LABELS_KEY, { c1: "Acme Corp", c2: "Globex Inc" });
  return client;
}

const meta: Meta<typeof ListRenderer> = {
  title: "Renderers/ListRenderer",
  component: ListRenderer,
  args: { view, module: MODULE },
};

export default meta;

type Story = StoryObj<typeof ListRenderer>;

// Storybook composes decorators across meta/story levels rather than
// overriding — each story below sets its own provider stack explicitly
// (a distinct QueryClient/router-path per story) instead of a shared
// meta-level one, so no story ends up double-wrapped.
export const Default: Story = {
  name: "full-page mode, every column type",
  decorators: [withListProviders(defaultClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table", { name: "Orders" }));
    const body = within(table);

    await expect(body.getByText("SO-1042")).toBeInTheDocument();
    // Locale-formatted like column-renderers.tsx's own currency case,
    // rather than a hardcoded "$450.50" that only matches en-US.
    await expect(
      body.getByText(new Intl.NumberFormat(undefined, { style: "currency", currency: "USD" }).format(450.5)),
    ).toBeInTheDocument();
    await expect(body.getByText("Confirmed")).toBeInTheDocument();
    await expect(body.getByText("Done")).toBeInTheDocument();
    // Relation label resolved via the seeded useRelationLabels cache, not the raw customer id.
    await expect(body.getByText("Acme Corp")).toBeInTheDocument();
    await expect(body.getByText("Globex Inc")).toBeInTheDocument();

    await expect(canvas.getByText("Active")).toBeInTheDocument(); // the boolean filter
    await expect(canvas.getByRole("button", { name: "New Order" })).toBeInTheDocument();

    // BulkActions integration: selecting a row surfaces the export action.
    const rowCheckboxes = canvas.getAllByRole("checkbox", { name: "Select row" });
    await userEvent.click(rowCheckboxes[0] as HTMLElement);
    await expect(canvas.getByText("1 selected")).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Export Selected" })).toBeInTheDocument();
  },
};

// A prefetch that never resolves registers the query as already "fetching"
// — ListRenderer's own useInfiniteList mounts against the same key and
// subscribes to that in-flight (forever-pending) fetch instead of issuing
// a second one, so the loading state is stable rather than racing a real
// (unmocked) network call to an error.
function loadingClient(): QueryClient {
  const client = seededClient();
  void client.prefetchInfiniteQuery({
    queryKey: infiniteListKey({}),
    queryFn: () => new Promise<never>(() => {}),
    initialPageParam: undefined,
    getNextPageParam: () => undefined,
  });
  return client;
}

export const Loading: Story = {
  decorators: [withListProviders(loadingClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("status", { name: "Loading Orders" })).toBeInTheDocument();
  },
};

function emptyClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(infiniteListKey({}), pageOf([]));
  return client;
}

export const Empty: Story = {
  decorators: [withListProviders(emptyClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("No orders found.")).toBeInTheDocument();
  },
};

export const GroupedByState: Story = {
  name: "group_by_options: grouped into one table per state",
  decorators: [withListProviders(defaultClient(), "/?group_by=state")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect((canvas.getByRole("combobox", { name: "Group by" }) as HTMLSelectElement).value).toBe("state");
    const tables = await waitFor(() => canvas.getAllByRole("table"));
    await expect(tables).toHaveLength(2);
    await expect(canvas.getByText("state = confirmed")).toBeInTheDocument();
    await expect(canvas.getByText("state = done")).toBeInTheDocument();
  },
};

// A column the fixture user lacks field-level read access to is omitted
// from the DOM entirely, not rendered blank — use-visible-columns.ts's own
// AC. checkField denies by default for any field absent from fieldAccess
// (permission-provider.tsx), so every other rendered field needs an
// explicit read:true grant here — the global Storybook decorator's
// always-allow context (preview.tsx) is overridden for this one story.
function fieldAccessDenying(deniedField: string) {
  const access: Record<string, { read: boolean; write: boolean }> = {};
  for (const column of COLUMNS) {
    access[column.field] = { read: column.field !== deniedField, write: false };
  }
  return createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: { "sales.order": access },
    modulesEnabled: new Set(),
  });
}

export const FieldPermissionRedaction: Story = {
  name: "a field-security-denied column is omitted, not blanked",
  decorators: [
    withListProviders(defaultClient(), "/"),
    (Story) => (
      <PermissionContext.Provider value={fieldAccessDenying("internal_note")}>
        <Story />
      </PermissionContext.Provider>
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table", { name: "Orders" }));
    const body = within(table);
    await expect(body.getByText("Reference")).toBeInTheDocument();
    await expect(body.queryByText("Note")).not.toBeInTheDocument();
    await expect(body.queryByText("Called customer to confirm address.")).not.toBeInTheDocument();
  },
};

const RECORD_ID = "cust-500";
const EMBEDDED_FILTER = { customer_id: RECORD_ID };

function embeddedClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(infiniteListKey(EMBEDDED_FILTER, `embedded:${RECORD_ID}:${view.name}`), pageOf(ROWS));
  client.setQueryData(RELATION_LABELS_KEY, { c1: "Acme Corp", c2: "Globex Inc" });
  return client;
}

export const Embedded: Story = {
  name: "embedded mode (sub_list): locked baseFilter, local state, no URL echo",
  args: { embedded: true, baseFilter: EMBEDDED_FILTER, recordId: RECORD_ID },
  decorators: [withListProviders(embeddedClient(), "/")],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const table = await waitFor(() => canvas.getByRole("table", { name: "Orders" }));
    await expect(within(table).getByText("SO-1042")).toBeInTheDocument();
  },
};
