import { describe, expect, it } from "vitest";
import { buildPivotSheetData } from "./pivot-download.js";
import type { PivotHeaderNode } from "./pivot-view-types.js";

const rowHeaders: PivotHeaderNode[] = [
  {
    key: '["east"]',
    label: "east",
    accessibleLabel: "east",
    children: [
      { key: '["east","widgets"]', label: "widgets", accessibleLabel: "widgets" },
      { key: '["east","gadgets"]', label: "gadgets", accessibleLabel: "gadgets" },
    ],
  },
  { key: '["west"]', label: "west", accessibleLabel: "west" },
];

const columnHeaders: PivotHeaderNode[] = [{ key: '["confirmed"]', label: "confirmed", accessibleLabel: "confirmed" }];

const values = [{ key: "amount_sum", label: "Revenue" }];

describe("buildPivotSheetData", () => {
  it("builds a header row and one body row per leaf, ignoring intermediate subtotal nodes", () => {
    const cells = [
      { rowKey: '["east","widgets"]', columnKey: '["confirmed"]', valueKey: "amount_sum", value: 100 },
      { rowKey: '["east","gadgets"]', columnKey: '["confirmed"]', valueKey: "amount_sum", value: 30 },
      { rowKey: '["east"]', columnKey: '["confirmed"]', valueKey: "amount_sum", value: 130 },
      { rowKey: '["west"]', columnKey: '["confirmed"]', valueKey: "amount_sum", value: 200 },
    ];

    const sheet = buildPivotSheetData(["region", "category"], rowHeaders, columnHeaders, values, cells);

    expect(sheet[0]).toEqual(["region", "category", "confirmed — Revenue"]);
    // "west" has no children — visibleLeaves treats it as its own leaf,
    // matching a shallower branch of an otherwise-nested axis.
    expect(sheet).toContainEqual(["east", "widgets", 100]);
    expect(sheet).toContainEqual(["east", "gadgets", 30]);
    expect(sheet).toContainEqual(["west", 200]);
    // The "east" subtotal-only cell (rowKey '["east"]') has no leaf of its
    // own once "east" has children — it's covered by widgets/gadgets
    // above, not re-emitted as a body row.
    expect(sheet).toHaveLength(4);
  });

  it("falls back to a blank leading column label when no row dimension is declared", () => {
    const sheet = buildPivotSheetData([], [], columnHeaders, values, [
      { rowKey: "[]", columnKey: '["confirmed"]', valueKey: "amount_sum", value: 10 },
    ]);
    expect(sheet[0]).toEqual(["", "confirmed — Revenue"]);
  });

  it("fills a missing cell with null rather than omitting the column", () => {
    const sheet = buildPivotSheetData(
      ["region"],
      [{ key: '["east"]', label: "east", accessibleLabel: "east" }],
      columnHeaders,
      values,
      [],
    );
    expect(sheet[1]).toEqual(["east", null]);
  });
});
