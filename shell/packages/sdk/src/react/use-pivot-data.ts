import { useQuery } from "@tanstack/react-query";
import type { FilterParamValue } from "../http/filter-params.js";
import { flattenFilterParams } from "../http/filter-params.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

export interface PivotValueSpec {
  field: string;
  aggregation: "sum" | "count" | "avg" | "min" | "max" | "count_distinct";
}

// view-system.md §8 "use_wasm: false" — one entry per GROUP BY ROLLUP
// result row. A `null` at a `row`/`column` position marks a rolled-up
// subtotal covering that axis position and everything nested beneath it,
// not a genuinely null grouped value (the backend's GROUPING() check
// already disambiguates the two before this ever reaches the client).
export interface PivotCellResponse {
  row: (string | number | boolean | null)[];
  column: (string | number | boolean | null)[];
  values: Record<string, number | string | null>;
}

export interface PivotResponse {
  cells: PivotCellResponse[];
}

export interface UsePivotDataOptions {
  rows: string[];
  columns: string[];
  values: PivotValueSpec[];
  filter?: Record<string, FilterParamValue>;
  // Same purpose as useInfiniteList's cacheKeyPrefix — view-system.md's
  // embedded-rendering contract needs an isolated cache key per embedded
  // instance.
  cacheKeyPrefix?: string;
}

function valuesParam(values: PivotValueSpec[]): string {
  return values.map((v) => `${v.field}:${v.aggregation}`).join(",");
}

export function createPivotDataQueryOptions(
  resource: string,
  options: UsePivotDataOptions,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: [
      "pivot-data",
      options.cacheKeyPrefix ?? null,
      resource,
      options.rows,
      options.columns,
      options.values,
      options.filter ?? null,
    ],
    queryFn: async (): Promise<PivotResponse> => {
      const entry = await registry.resolve(resource);
      if (entry.pivotPath === null) {
        throw new Error(`usePivotData: resource "${resource}" declares no pivot route`);
      }
      return client.get<PivotResponse>(entry.pivotPath, {
        params: {
          ...(options.rows.length > 0 ? { rows: options.rows.join(",") } : {}),
          ...(options.columns.length > 0 ? { columns: options.columns.join(",") } : {}),
          values: valuesParam(options.values),
          ...flattenFilterParams(options.filter),
        },
      });
    },
    enabled: options.rows.length > 0 || options.columns.length > 0,
  };
}

export function usePivotData(resource: string, options: UsePivotDataOptions) {
  return useQuery(createPivotDataQueryOptions(resource, options));
}
