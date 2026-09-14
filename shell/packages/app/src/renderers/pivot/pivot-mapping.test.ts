import { describe, expect, it } from "vitest";
import { mapPivotResponse, toValueColumns } from "./pivot-mapping.js";

describe("mapPivotResponse", () => {
  it("builds nested header trees and leaf cells from a two-axis response", () => {
    const result = mapPivotResponse(
      {
        cells: [
          { row: ["east", "widgets"], column: ["confirmed"], values: { amount_sum: 100 } },
          { row: ["east", "gadgets"], column: ["confirmed"], values: { amount_sum: 30 } },
          { row: ["east", null], column: ["confirmed"], values: { amount_sum: 130 } },
          { row: ["west", "widgets"], column: ["confirmed"], values: { amount_sum: 200 } },
          { row: ["west", null], column: ["confirmed"], values: { amount_sum: 200 } },
          { row: [null, null], column: ["confirmed"], values: { amount_sum: 330 } },
          { row: [null, null], column: [null], values: { amount_sum: 330 } },
        ],
      },
      ["region", "category"],
      ["state"],
    );

    expect(result.rowHeaders).toEqual([
      {
        key: '["east"]',
        label: "east",
        accessibleLabel: "east",
        children: [
          { key: '["east","widgets"]', label: "widgets", accessibleLabel: "widgets" },
          { key: '["east","gadgets"]', label: "gadgets", accessibleLabel: "gadgets" },
        ],
      },
      {
        key: '["west"]',
        label: "west",
        accessibleLabel: "west",
        children: [{ key: '["west","widgets"]', label: "widgets", accessibleLabel: "widgets" }],
      },
    ]);
    expect(result.columnHeaders).toEqual([{ key: '["confirmed"]', label: "confirmed", accessibleLabel: "confirmed" }]);

    // The row=[null,null] grand-total-across-rows cell (column still
    // "confirmed") has no row header node to attach to and is dropped,
    // as is the fully-null overall grand total.
    expect(result.cells).toHaveLength(5);
    expect(result.cells).toContainEqual({
      rowKey: '["east","widgets"]',
      columnKey: '["confirmed"]',
      valueKey: "amount_sum",
      value: 100,
    });
    expect(result.cells).toContainEqual({
      rowKey: '["east"]',
      columnKey: '["confirmed"]',
      valueKey: "amount_sum",
      value: 130,
    });
  });

  it("coerces a numeric-string aggregate (e.g. AVG) to a number", () => {
    const result = mapPivotResponse(
      { cells: [{ row: ["east"], column: [], values: { amount_avg: "135.0000000000000000" } }] },
      ["region"],
      [],
    );
    expect(result.cells[0]?.value).toBe(135);
  });

  it("maps a null aggregate value to null rather than NaN", () => {
    const result = mapPivotResponse(
      { cells: [{ row: ["east"], column: [], values: { amount_sum: null } }] },
      ["region"],
      [],
    );
    expect(result.cells[0]?.value).toBeNull();
  });

  it("returns empty headers/cells for an empty response", () => {
    expect(mapPivotResponse({ cells: [] }, ["region"], ["state"])).toEqual({
      rowHeaders: [],
      columnHeaders: [],
      cells: [],
    });
  });

  it("synthesizes a single Total node for a declared-empty axis, keyed to match every cell's own key", () => {
    const result = mapPivotResponse(
      { cells: [{ row: [], column: ["confirmed"], values: { amount_sum: 10 } }] },
      [],
      ["state"],
    );
    expect(result.rowHeaders).toEqual([{ key: "[]", label: "Total", accessibleLabel: "Total" }]);
    expect(result.cells).toEqual([{ rowKey: "[]", columnKey: '["confirmed"]', valueKey: "amount_sum", value: 10 }]);
  });

  it("synthesizes a Total node on both axes for a totals-only (no rows, no columns) pivot", () => {
    const result = mapPivotResponse({ cells: [{ row: [], column: [], values: { amount_sum: 999 } }] }, [], []);
    expect(result.rowHeaders).toEqual([{ key: "[]", label: "Total", accessibleLabel: "Total" }]);
    expect(result.columnHeaders).toEqual([{ key: "[]", label: "Total", accessibleLabel: "Total" }]);
    expect(result.cells).toEqual([{ rowKey: "[]", columnKey: "[]", valueKey: "amount_sum", value: 999 }]);
  });
});

describe("toValueColumns", () => {
  it("keys each value column by field_aggregation and falls back label to field", () => {
    expect(
      toValueColumns([
        { field: "amount_total", aggregation: "sum", label: "Revenue", format: "currency" },
        { field: "id", aggregation: "count" },
      ]),
    ).toEqual([
      { key: "amount_total_sum", label: "Revenue", format: "currency" },
      { key: "id_count", label: "id" },
    ]);
  });
});
