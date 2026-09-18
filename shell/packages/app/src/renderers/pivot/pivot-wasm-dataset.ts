import type { PivotResponse, PivotValueSpec } from "@goerp/sdk/react";
import { getDuckDB } from "./pivot-duckdb-runtime.js";
import { buildPivotAggregateSQL, mapDuckDBRowsToPivotResponse } from "./pivot-wasm-sql.js";

export interface PivotWasmDataset {
  aggregate(rows: string[], columns: string[], values: PivotValueSpec[]): Promise<PivotResponse>;
  dispose(): Promise<void>;
}

// Loads a set of already-server-filtered Parquet pages into DuckDB-WASM
// once, then lets the caller re-run pivot aggregation queries against the
// same in-memory dataset on every rows/columns/values change with no new
// fetch — view-system.md §8's use_wasm:true contract. Each page is its
// own valid Parquet file with its own footer, so pages are registered as
// separate virtual files and queried together via read_parquet([...]),
// not byte-concatenated.
export async function loadPivotWasmDataset(pages: Uint8Array[]): Promise<PivotWasmDataset> {
  const db = await getDuckDB();
  const conn = await db.connect();
  const fileNames = pages.map((_, i) => `pivot-page-${i}-${Math.random().toString(36).slice(2)}.parquet`);
  for (const [i, page] of pages.entries()) {
    await db.registerFileBuffer(fileNames[i] as string, page);
  }
  const fromClause = `read_parquet([${fileNames.map((n) => `'${n}'`).join(", ")}])`;

  return {
    async aggregate(rows, columns, values) {
      if (rows.length === 0 && columns.length === 0) return { cells: [] };
      const { sql, valueAliases } = buildPivotAggregateSQL(fromClause, rows, columns, values);
      const table = await conn.query(sql);
      const duckRows = table.toArray().map((row) => row.toJSON() as Record<string, unknown>);
      return mapDuckDBRowsToPivotResponse(duckRows, rows, columns, valueAliases);
    },
    async dispose() {
      await conn.close();
      await Promise.all(fileNames.map((n) => db.dropFile(n)));
    },
  };
}
