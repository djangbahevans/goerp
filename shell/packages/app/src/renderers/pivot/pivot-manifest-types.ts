import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";
import { ListFilterSchema } from "../list/list-view-types.js";

const PIVOT_AGGREGATIONS = ["sum", "count", "avg", "min", "max", "count_distinct"] as const;
export type PivotAggregation = (typeof PIVOT_AGGREGATIONS)[number];

export const PivotValueSchema = v.looseObject({
  field: v.string(),
  aggregation: v.picklist(PIVOT_AGGREGATIONS),
  label: opt(v.string()),
  format: opt(v.picklist(["currency", "percent", "number"] as const)),
});
export type PivotValue = v.InferOutput<typeof PivotValueSchema>;

// manifest-spec.md §9.5's Pivot View wire schema, resolved by
// pivot-renderer.tsx into PivotViewProps (pivot-view-types.ts).
export const PivotViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("pivot"),
  resource: v.string(),
  label: v.string(),
  permission: opt(v.string()),
  rows: v.array(v.string()),
  columns: v.array(v.string()),
  values: v.array(PivotValueSchema),
  default_filters: opt(v.record(v.string(), v.unknown())),
  filters: opt(v.array(ListFilterSchema)),
  allow_download: opt(v.boolean()),
  use_wasm: opt(v.boolean()),
});
export type PivotViewDeclaration = v.InferOutput<typeof PivotViewDeclarationSchema>;
