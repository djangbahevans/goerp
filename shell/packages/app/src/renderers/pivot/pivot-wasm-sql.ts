import type { PivotCellResponse, PivotResponse, PivotValueSpec } from "@goerp/sdk/react";

// Mirrors host_orm.go's ORMAggregate (aggregateSQLFuncs) — "count_distinct"
// isn't here since it needs a DISTINCT keyword inside the call, not a
// different function name.
const AGGREGATE_SQL_FUNCS: Record<string, string> = {
  sum: "SUM",
  count: "COUNT",
  avg: "AVG",
  min: "MIN",
  max: "MAX",
};

function quoteIdent(name: string): string {
  return `"${name.replace(/"/g, '""')}"`;
}

function groupingAlias(field: string): string {
  return `__grouping_${field}`;
}

export interface PivotAggregateSQL {
  sql: string;
  valueAliases: string[];
}

// Mirrors ORMAggregate's query shape exactly (down to the __grouping_
// alias naming) so the two implementations stay easy to compare: one
// GROUP BY ROLLUP(rows), ROLLUP(columns) query over the already-fetched,
// already-filtered Parquet pages produces every row/column subtotal and
// the grand total in a single pass, and GROUPING() on each dimension
// column disambiguates a rolled-up position from a genuinely NULL value.
export function buildPivotAggregateSQL(
  fromClause: string,
  rows: string[],
  columns: string[],
  values: PivotValueSpec[],
): PivotAggregateSQL {
  const dimFields = [...rows, ...columns];
  const selectExprs: string[] = dimFields.map(quoteIdent);

  const valueAliases: string[] = [];
  for (const v of values) {
    const alias = `${v.field}_${v.aggregation}`;
    valueAliases.push(alias);
    let expr: string;
    if (v.aggregation === "count_distinct") {
      expr = `COUNT(DISTINCT ${quoteIdent(v.field)})`;
    } else {
      const fn = AGGREGATE_SQL_FUNCS[v.aggregation];
      // Manifest parsing already rejects an aggregation outside this set
      // (pivot-manifest-types.ts's picklist) — this only guards a value
      // reaching here some other way, matching ORMAggregate's own
      // up-front validation rather than letting it become an opaque
      // DuckDB parser error on `undefined(...)`.
      if (fn === undefined) throw new Error(`buildPivotAggregateSQL: unknown aggregation "${v.aggregation}"`);
      expr = `${fn}(${quoteIdent(v.field)})`;
    }
    selectExprs.push(`${expr} AS ${quoteIdent(alias)}`);
  }

  for (const f of dimFields) {
    selectExprs.push(`(GROUPING(${quoteIdent(f)}) = 1) AS ${quoteIdent(groupingAlias(f))}`);
  }

  let groupByClause: string;
  if (rows.length > 0 && columns.length > 0) {
    groupByClause = `GROUP BY ROLLUP(${rows.map(quoteIdent).join(", ")}), ROLLUP(${columns.map(quoteIdent).join(", ")})`;
  } else if (rows.length > 0) {
    groupByClause = `GROUP BY ROLLUP(${rows.map(quoteIdent).join(", ")})`;
  } else {
    groupByClause = `GROUP BY ROLLUP(${columns.map(quoteIdent).join(", ")})`;
  }

  return { sql: `SELECT ${selectExprs.join(", ")} FROM ${fromClause} ${groupByClause}`, valueAliases };
}

function dimensionValues(row: Record<string, unknown>, fields: string[]): (string | number | boolean | null)[] {
  return fields.map((f) => {
    if (row[groupingAlias(f)] === true) return null;
    return row[f] as string | number | boolean | null;
  });
}

function normalizeCellValue(raw: unknown): number | string | null {
  if (raw === null || raw === undefined) return null;
  if (typeof raw === "bigint") return Number(raw);
  if (typeof raw === "number" || typeof raw === "string") return raw;
  return Number(raw);
}

// Reshapes DuckDB's flat GROUP BY ROLLUP result rows into the same
// PivotResponse shape the server's use_wasm:false route returns
// (view-system.md §8), so mapPivotResponse (pivot-mapping.ts) needs no
// separate code path for the WASM source.
export function mapDuckDBRowsToPivotResponse(
  duckRows: Record<string, unknown>[],
  rows: string[],
  columns: string[],
  valueAliases: string[],
): PivotResponse {
  const cells: PivotCellResponse[] = duckRows.map((row) => {
    const values: Record<string, number | string | null> = {};
    for (const alias of valueAliases) values[alias] = normalizeCellValue(row[alias]);
    return { row: dimensionValues(row, rows), column: dimensionValues(row, columns), values };
  });
  return { cells };
}
