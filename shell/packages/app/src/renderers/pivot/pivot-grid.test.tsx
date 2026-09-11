import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PivotGrid } from "./pivot-grid.js";
import type { PivotCell, PivotHeaderNode, PivotValueColumn } from "./pivot-view-types.js";

afterEach(cleanup);

// view-system.md §8's sales_pivot example: rows: [customer_name],
// columns: [state], values: [amount_total:sum:Revenue:currency,
// id:count:Order Count:number] — extended with a second rows level
// (region > customer) to exercise nesting/collapse.
function rowHeaders(): PivotHeaderNode[] {
  return [
    {
      key: "emea",
      label: "EMEA",
      accessibleLabel: "EMEA",
      children: [
        { key: "emea.acme", label: "Acme Corp", accessibleLabel: "Acme Corp" },
        { key: "emea.globex", label: "Globex Inc", accessibleLabel: "Globex Inc" },
      ],
    },
  ];
}

function columnHeaders(): PivotHeaderNode[] {
  return [
    { key: "confirmed", label: "Confirmed", accessibleLabel: "Confirmed" },
    { key: "done", label: "Done", accessibleLabel: "Done" },
  ];
}

function values(): PivotValueColumn[] {
  return [
    { key: "amount_total", label: "Revenue", format: "currency", currency: "USD" },
    { key: "id", label: "Order Count", format: "number" },
  ];
}

// l10n-guide.md: monetary amounts are integer minor units — $45,000.00 is
// 4500000, not 45000.
function cells(): PivotCell[] {
  return [
    { rowKey: "emea.acme", columnKey: "confirmed", valueKey: "amount_total", value: 4_500_000 },
    { rowKey: "emea.acme", columnKey: "confirmed", valueKey: "id", value: 3 },
    { rowKey: "emea.acme", columnKey: "done", valueKey: "amount_total", value: 1_200_000 },
    { rowKey: "emea.acme", columnKey: "done", valueKey: "id", value: 1 },
    { rowKey: "emea.globex", columnKey: "confirmed", valueKey: "amount_total", value: 800_000 },
    { rowKey: "emea.globex", columnKey: "confirmed", valueKey: "id", value: 1 },
    { rowKey: "emea.globex", columnKey: "done", valueKey: "amount_total", value: 0 },
    { rowKey: "emea.globex", columnKey: "done", valueKey: "id", value: 0 },
    { rowKey: "emea", columnKey: "confirmed", valueKey: "amount_total", value: 5_300_000 },
    { rowKey: "emea", columnKey: "confirmed", valueKey: "id", value: 4 },
    { rowKey: "emea", columnKey: "done", valueKey: "amount_total", value: 1_200_000 },
    { rowKey: "emea", columnKey: "done", valueKey: "id", value: 1 },
  ];
}

describe("PivotGrid", () => {
  it("renders resolved row/column headers and formatted cell values", () => {
    render(<PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={values()} cells={cells()} />);
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.getByText("Confirmed")).toBeTruthy();
    expect(screen.getByText("$45,000.00")).toBeTruthy();
  });

  it("shows a value sub-header row when there is more than one value column", () => {
    render(<PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={values()} cells={cells()} />);
    expect(screen.getAllByText("Revenue").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Order Count").length).toBeGreaterThan(0);
  });

  it("announces each cell's resolved row/column header context, not a bare number", () => {
    render(<PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={values()} cells={cells()} />);
    const cell = screen.getByText("$45,000.00").closest("td");
    expect(cell).not.toBeNull();
    expect(within(cell as HTMLElement).getByText(/EMEA, Acme Corp, Confirmed, Revenue:/)).toBeTruthy();
  });

  it("collapses a row group behind a real button with aria-expanded, hiding its children", () => {
    render(<PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={values()} cells={cells()} />);
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    const toggle = screen.getByRole("button", { name: "Collapse EMEA" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(screen.queryByText("Acme Corp")).toBeNull();
    expect(screen.getByText("EMEA")).toBeTruthy();
    // The collapsed group's own rollup cell (keyed by "emea"), not a
    // leftover child value or a dropped cellMap lookup.
    expect(screen.getByText("$53,000.00")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Expand EMEA" }).getAttribute("aria-expanded")).toBe("false");
  });

  it("falls back to the em dash for a cell with no aggregated value", () => {
    const sparseCells = cells().filter((cell) => cell.rowKey !== "emea.globex" || cell.columnKey !== "done");
    render(
      <PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={values()} cells={sparseCells} />,
    );
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  it("renders a table Skeleton while loading", () => {
    render(<PivotGrid rowHeaders={[]} columnHeaders={[]} values={values()} cells={[]} isLoading />);
    expect(document.querySelector("[data-skeleton='table']")).toBeTruthy();
  });

  it("renders EmptyState when there are no rows or columns to show", () => {
    render(<PivotGrid rowHeaders={[]} columnHeaders={[]} values={values()} cells={[]} />);
    expect(screen.getByText("No results")).toBeTruthy();
  });

  it("renders EmptyState instead of a headers-with-no-columns table when values is empty", () => {
    render(<PivotGrid rowHeaders={rowHeaders()} columnHeaders={columnHeaders()} values={[]} cells={[]} />);
    expect(screen.getByText("No results")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows a status region while recomputing", () => {
    render(
      <PivotGrid
        rowHeaders={rowHeaders()}
        columnHeaders={columnHeaders()}
        values={values()}
        cells={cells()}
        isRecomputing
      />,
    );
    expect(screen.getByRole("status")).toBeTruthy();
  });

  it("doesn't collide cells whose row/column keys happen to share a delimiter-like substring", () => {
    // "east|west" as a rowKey vs. "west" split across rowKey/columnKey would
    // collide under a bare "|"-joined cell-map key.
    const rows: PivotHeaderNode[] = [{ key: "east|west", label: "East-West", accessibleLabel: "East-West" }];
    const columns: PivotHeaderNode[] = [
      { key: "q1", label: "Q1", accessibleLabel: "Q1" },
      { key: "west|q1", label: "West-Q1", accessibleLabel: "West-Q1" },
    ];
    const collidingCells: PivotCell[] = [
      { rowKey: "east|west", columnKey: "q1", valueKey: "amount_total", value: 100 },
      { rowKey: "east", columnKey: "west|q1", valueKey: "amount_total", value: 999 },
    ];
    render(
      <PivotGrid
        rowHeaders={rows}
        columnHeaders={columns}
        values={[{ key: "amount_total", label: "Revenue", format: "number" }]}
        cells={collidingCells}
      />,
    );
    expect(screen.getByText("100")).toBeTruthy();
    expect(screen.queryByText("999")).toBeNull();
  });
});
