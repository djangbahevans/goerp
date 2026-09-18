import { describe, expect, it } from "vitest";
import { buildPivotAggregateSQL, mapDuckDBRowsToPivotResponse } from "./pivot-wasm-sql.js";

describe("buildPivotAggregateSQL", () => {
  it("builds a ROLLUP query over both axes with GROUPING columns and value aggregates", () => {
    const { sql, valueAliases } = buildPivotAggregateSQL(
      "read_parquet(['a.parquet'])",
      ["region"],
      ["state"],
      [
        { field: "amount_total", aggregation: "sum" },
        { field: "id", aggregation: "count" },
      ],
    );

    expect(valueAliases).toEqual(["amount_total_sum", "id_count"]);
    expect(sql).toContain('"region"');
    expect(sql).toContain('"state"');
    expect(sql).toContain('SUM("amount_total") AS "amount_total_sum"');
    expect(sql).toContain('COUNT("id") AS "id_count"');
    expect(sql).toContain('(GROUPING("region") = 1) AS "__grouping_region"');
    expect(sql).toContain('(GROUPING("state") = 1) AS "__grouping_state"');
    expect(sql).toContain('GROUP BY ROLLUP("region"), ROLLUP("state")');
    expect(sql).toContain("FROM read_parquet(['a.parquet'])");
  });

  it("uses a single ROLLUP when only rows are declared", () => {
    const { sql } = buildPivotAggregateSQL("t", ["region"], [], [{ field: "id", aggregation: "count" }]);
    expect(sql).toContain('GROUP BY ROLLUP("region")');
    expect(sql).not.toContain("ROLLUP(),");
  });

  it("uses a single ROLLUP when only columns are declared", () => {
    const { sql } = buildPivotAggregateSQL("t", [], ["state"], [{ field: "id", aggregation: "count" }]);
    expect(sql).toContain('GROUP BY ROLLUP("state")');
  });

  it("uses COUNT(DISTINCT ...) for count_distinct", () => {
    const { sql } = buildPivotAggregateSQL(
      "t",
      ["region"],
      [],
      [{ field: "customer_id", aggregation: "count_distinct" }],
    );
    expect(sql).toContain('COUNT(DISTINCT "customer_id") AS "customer_id_count_distinct"');
  });

  it("escapes double quotes in field names", () => {
    const { sql } = buildPivotAggregateSQL("t", ['weird"field'], [], [{ field: "id", aggregation: "count" }]);
    expect(sql).toContain('"weird""field"');
  });
});

describe("mapDuckDBRowsToPivotResponse", () => {
  it("replaces a rolled-up dimension with null per its GROUPING flag", () => {
    const rows = [
      { region: "east", state: "confirmed", amount_total_sum: 150, __grouping_region: false, __grouping_state: false },
      { region: "east", state: null, amount_total_sum: 300, __grouping_region: false, __grouping_state: true },
      { region: null, state: null, amount_total_sum: 900, __grouping_region: true, __grouping_state: true },
    ];

    const response = mapDuckDBRowsToPivotResponse(rows, ["region"], ["state"], ["amount_total_sum"]);

    expect(response.cells).toEqual([
      { row: ["east"], column: ["confirmed"], values: { amount_total_sum: 150 } },
      { row: ["east"], column: [null], values: { amount_total_sum: 300 } },
      { row: [null], column: [null], values: { amount_total_sum: 900 } },
    ]);
  });

  it("converts BigInt aggregate values (DuckDB's COUNT type) to Number", () => {
    const rows = [{ region: "east", id_count: 3n, __grouping_region: false }];
    const response = mapDuckDBRowsToPivotResponse(rows, ["region"], [], ["id_count"]);
    expect(response.cells[0]?.values.id_count).toBe(3);
    expect(typeof response.cells[0]?.values.id_count).toBe("number");
  });

  it("maps a null aggregate value through as null", () => {
    const rows = [{ region: "east", amount_total_sum: null, __grouping_region: false }];
    const response = mapDuckDBRowsToPivotResponse(rows, ["region"], [], ["amount_total_sum"]);
    expect(response.cells[0]?.values.amount_total_sum).toBeNull();
  });
});
