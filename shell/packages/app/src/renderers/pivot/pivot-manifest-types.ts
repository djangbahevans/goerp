import type { ListFilter } from "../list/list-view-types.js";

export type PivotAggregation = "sum" | "count" | "avg" | "min" | "max" | "count_distinct";

export interface PivotValue {
  field: string;
  aggregation: PivotAggregation;
  label?: string;
  format?: "currency" | "percent" | "number";
}

// manifest-spec.md §9.5's Pivot View wire schema — the manifest fields
// PivotRenderer resolves into PivotViewProps (pivot-view-types.ts), the
// concern pivot-view-types.ts's own top comment explicitly defers to here.
// default_filters/filters reuse ListViewDeclaration's exact shape (same
// boolean-slice filter primitives, list-renderer.tsx's computeDefaultFilters
// and list-filters.tsx's ListFilters).
export interface PivotViewDeclaration {
  name: string;
  type: "pivot";
  resource: string;
  label: string;
  permission?: string;
  rows: string[];
  columns: string[];
  values: PivotValue[];
  default_filters?: Record<string, unknown>;
  filters?: ListFilter[];
  allow_download?: boolean;
  use_wasm?: boolean;
}
