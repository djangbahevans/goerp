import type { FilterParamValue } from "@goerp/sdk";
import { apiClient, fetchAllParquetPages } from "@goerp/sdk";
import type { PivotResponse, PivotValueSpec } from "@goerp/sdk/react";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useEffect, useRef, useState } from "react";
import { loadPivotWasmDataset, type PivotWasmDataset } from "./pivot-wasm-dataset.js";

export interface UsePivotWasmDataOptions {
  rows: string[];
  columns: string[];
  values: PivotValueSpec[];
  filter?: Record<string, FilterParamValue>;
  cacheKeyPrefix?: string;
}

export interface UsePivotWasmDataResult {
  data: PivotResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => void;
}

function toError(err: unknown): Error {
  return err instanceof Error ? err : new Error(String(err));
}

// view-system.md §8's use_wasm:true path: fetches the full filtered
// dataset as Parquet once per resource/filter change (paging through
// fetchAllParquetPages), loads it into DuckDB-WASM, and re-runs the pivot
// aggregation client-side on every rows/columns/values change with no new
// request. Exposes the same {data, isLoading, isError, error, refetch}
// shape as usePivotData so pivot-renderer.tsx can branch on view.use_wasm
// with minimal churn.
export function usePivotWasmData(resource: string, options: UsePivotWasmDataOptions): UsePivotWasmDataResult {
  const [state, setState] = useState<{ data?: PivotResponse; isLoading: boolean; error: Error | null }>({
    isLoading: true,
    error: null,
  });
  const [reloadToken, setReloadToken] = useState(0);
  const datasetRef = useRef<PivotWasmDataset | null>(null);
  const datasetKeyRef = useRef<string | null>(null);

  const rows = options.rows;
  const columns = options.columns;
  const values = options.values;
  const enabled = rows.length > 0 || columns.length > 0;
  const filterKey = JSON.stringify(options.filter ?? null);
  // Identifies which (resource, filter) generation the loaded dataset
  // belongs to — a rows/columns/values-only change re-runs this effect
  // (they're in the dependency list below) but this key stays the same,
  // so the network fetch is skipped and only the in-memory query re-runs.
  const datasetKey = `${resource}::${filterKey}::${options.cacheKeyPrefix ?? ""}::${reloadToken}`;
  const rowsKey = JSON.stringify(rows);
  const columnsKey = JSON.stringify(columns);
  const valuesKey = JSON.stringify(values);

  // biome-ignore lint/correctness/useExhaustiveDependencies: rowsKey/columnsKey/valuesKey/datasetKey are the deliberate change-triggers (stable JSON strings), standing in for rows/columns/values/options.filter, whose object/array identities change every render and would defeat that stability if listed directly.
  useEffect(() => {
    if (!enabled) {
      setState({ data: { cells: [] }, isLoading: false, error: null });
      return;
    }

    let cancelled = false;
    setState((s) => (s.data === undefined ? { ...s, isLoading: true, error: null } : { ...s, error: null }));

    (async () => {
      if (datasetKeyRef.current !== datasetKey) {
        const entry = await resourceRegistry.resolve(resource);
        const pages = await fetchAllParquetPages(
          entry.listPath,
          options.filter !== undefined ? { filter: options.filter } : {},
          apiClient,
        );
        if (cancelled) return;
        const dataset = await loadPivotWasmDataset(pages);
        if (cancelled) {
          await dataset.dispose();
          return;
        }
        await datasetRef.current?.dispose();
        datasetRef.current = dataset;
        datasetKeyRef.current = datasetKey;
      }

      const dataset = datasetRef.current;
      if (!dataset) return;
      const result = await dataset.aggregate(rows, columns, values);
      if (cancelled) return;
      setState({ data: result, isLoading: false, error: null });
    })().catch((err: unknown) => {
      if (!cancelled) setState((s) => ({ ...s, isLoading: false, error: toError(err) }));
    });

    return () => {
      cancelled = true;
    };
  }, [resource, datasetKey, enabled, rowsKey, columnsKey, valuesKey]);

  useEffect(
    () => () => {
      void datasetRef.current?.dispose();
    },
    [],
  );

  return {
    data: state.data,
    isLoading: state.isLoading,
    isError: state.error !== null,
    error: state.error,
    refetch: () => setReloadToken((t) => t + 1),
  };
}
