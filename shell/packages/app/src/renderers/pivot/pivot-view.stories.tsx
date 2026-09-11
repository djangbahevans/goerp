import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { PivotView } from "./pivot-view.js";
import type { PivotCell, PivotHeaderNode, PivotValueColumn } from "./pivot-view-types.js";

// view-system.md §8's sales_pivot example (rows: [customer_name],
// columns: [state], values: [amount_total:sum:Revenue:currency,
// id:count:Order Count:number]) — extended with a second rows level
// (region > customer) and a second columns level (quarter > state) to
// exercise 2-level header nesting, since no documented example shows more
// than one field in rows/columns at once (pivot-view.md's own open
// question). Already-resolved presentational data — a real caller
// resolves each customer_name/state value via goerp#646/#648's renderer
// wiring, not this story.
const ROW_HEADERS: PivotHeaderNode[] = [
  {
    key: "emea",
    label: "EMEA",
    accessibleLabel: "EMEA",
    children: [
      { key: "emea.acme", label: "Acme Corp", accessibleLabel: "Acme Corp" },
      { key: "emea.globex", label: "Globex Inc", accessibleLabel: "Globex Inc" },
    ],
  },
  {
    key: "apac",
    label: "APAC",
    accessibleLabel: "APAC",
    children: [{ key: "apac.initech", label: "Initech", accessibleLabel: "Initech" }],
  },
];

const COLUMN_HEADERS: PivotHeaderNode[] = [
  {
    key: "q1",
    label: "Q1",
    accessibleLabel: "Q1",
    children: [
      { key: "q1.confirmed", label: "Confirmed", accessibleLabel: "Confirmed" },
      { key: "q1.done", label: "Done", accessibleLabel: "Done" },
    ],
  },
  {
    key: "q2",
    label: "Q2",
    accessibleLabel: "Q2",
    children: [
      { key: "q2.confirmed", label: "Confirmed", accessibleLabel: "Confirmed" },
      { key: "q2.done", label: "Done", accessibleLabel: "Done" },
    ],
  },
];

const VALUES: PivotValueColumn[] = [
  { key: "amount_total", label: "Revenue", format: "currency", currency: "USD" },
  { key: "id", label: "Order Count", format: "number" },
];

// l10n-guide.md: monetary amounts are integer minor units.
const REVENUE_BY_CELL: Record<string, Record<string, number>> = {
  "emea.acme": { "q1.confirmed": 4_500_000, "q1.done": 1_200_000, "q2.confirmed": 3_100_000, "q2.done": 900_000 },
  "emea.globex": { "q1.confirmed": 800_000, "q1.done": 0, "q2.confirmed": 1_050_000, "q2.done": 200_000 },
  "apac.initech": { "q1.confirmed": 2_200_000, "q1.done": 600_000, "q2.confirmed": 0, "q2.done": 0 },
};
const ORDER_COUNT_BY_CELL: Record<string, Record<string, number>> = {
  "emea.acme": { "q1.confirmed": 3, "q1.done": 1, "q2.confirmed": 2, "q2.done": 1 },
  "emea.globex": { "q1.confirmed": 1, "q1.done": 0, "q2.confirmed": 1, "q2.done": 1 },
  "apac.initech": { "q1.confirmed": 2, "q1.done": 1, "q2.confirmed": 0, "q2.done": 0 },
};

const ROW_SCOPES: Record<string, string[]> = {
  "emea.acme": ["emea.acme"],
  "emea.globex": ["emea.globex"],
  "apac.initech": ["apac.initech"],
  emea: ["emea.acme", "emea.globex"],
  apac: ["apac.initech"],
};
const COLUMN_SCOPES: Record<string, string[]> = {
  "q1.confirmed": ["q1.confirmed"],
  "q1.done": ["q1.done"],
  "q2.confirmed": ["q2.confirmed"],
  "q2.done": ["q2.done"],
  q1: ["q1.confirmed", "q1.done"],
  q2: ["q2.confirmed", "q2.done"],
};

// A mocked pre-aggregated result set stands in for a real DuckDB-WASM/
// server aggregation (goerp#646/#648's concern) — includes a subtotal for
// every (row scope, column scope) pair, leaf or group on either axis, not
// leaf×leaf only, since collapsing a group on either axis (or both at
// once) still needs a value to show for every remaining cell.
function mockCells(): PivotCell[] {
  const cells: PivotCell[] = [];
  for (const [rowKey, rowLeafKeys] of Object.entries(ROW_SCOPES)) {
    for (const [columnKey, columnLeafKeys] of Object.entries(COLUMN_SCOPES)) {
      let amount = 0;
      let count = 0;
      for (const rowLeafKey of rowLeafKeys) {
        for (const columnLeafKey of columnLeafKeys) {
          amount += REVENUE_BY_CELL[rowLeafKey]?.[columnLeafKey] ?? 0;
          count += ORDER_COUNT_BY_CELL[rowLeafKey]?.[columnLeafKey] ?? 0;
        }
      }
      cells.push({ rowKey, columnKey, valueKey: "amount_total", value: amount });
      cells.push({ rowKey, columnKey, valueKey: "id", value: count });
    }
  }
  return cells;
}

const meta: Meta<typeof PivotView> = {
  title: "Renderers/PivotView",
  component: PivotView,
  args: {
    title: "Sales Analysis",
    rowHeaders: ROW_HEADERS,
    columnHeaders: COLUMN_HEADERS,
    values: VALUES,
    cells: mockCells(),
    allowDownload: true,
    onDownload: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof PivotView>;

export const Default: Story = {
  name: "Loaded, expanded 2-level nesting",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Acme Corp")).toBeInTheDocument();
    await expect(canvas.getByText("$45,000.00")).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Download" })).toBeInTheDocument();
  },
};

export const CollapsedGroups: Story = {
  name: "Collapsed row and column groups",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Collapse EMEA" }));
    await userEvent.click(canvas.getByRole("button", { name: "Collapse Q1" }));
    await expect(canvas.queryByText("Acme Corp")).not.toBeInTheDocument();
    // EMEA's subtotal across all columns replaces its two customer rows.
    await expect(canvas.getByText("EMEA")).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Expand EMEA" })).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Expand Q1" })).toBeInTheDocument();
  },
};

export const Loading: Story = {
  name: "Loading (Parquet/Arrow download + WASM worker init, or server request in flight)",
  args: { isLoading: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Sales Analysis")).toBeInTheDocument();
    await expect(canvasElement.querySelector("[data-skeleton='table']")).toBeTruthy();
  },
};

export const Recomputing: Story = {
  name: "use_wasm: false recompute (subtle inline indicator, headers stay)",
  args: { isRecomputing: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Acme Corp")).toBeInTheDocument();
    await expect(canvas.getByRole("status")).toBeInTheDocument();
  },
};

export const EmptyResult: Story = {
  name: "Empty result set (filters exclude everything)",
  args: { rowHeaders: [], columnHeaders: [], cells: [] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("No results")).toBeInTheDocument();
  },
};

export const HorizontallyScrolled: Story = {
  name: "Horizontally scrolled — sticky row headers pick up the edge shadow",
  decorators: [
    (StoryComponent) => (
      <div style={{ maxWidth: 420 }}>
        <StoryComponent />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const scrollContainer = canvasElement.querySelector(".overflow-x-auto");
    if (!(scrollContainer instanceof HTMLElement)) throw new Error("Missing pivot grid scroll container");
    scrollContainer.scrollLeft = 200;
    scrollContainer.dispatchEvent(new Event("scroll", { bubbles: true }));
    await expect(canvas.getByText("Acme Corp")).toBeInTheDocument();
  },
};
