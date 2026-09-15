import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";
import { ListFilterSchema } from "../list/list-view-types.js";
import { ALL_TIMELINE_RANGES } from "./timeline-view-types.js";

// manifest-spec.md §9.6's Timeline View wire schema, resolved by
// timeline-renderer.tsx into TimelineChartProps.
export const TimelineViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("timeline"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  start_field: v.string(),
  end_field: v.string(),
  group_by: opt(v.string()),
  label_field: v.string(),
  color_field: opt(v.string()),
  color_map: opt(v.record(v.string(), v.string())),
  allow_drag: opt(v.boolean()),
  allow_resize: opt(v.boolean()),
  default_range: opt(v.picklist(ALL_TIMELINE_RANGES)),
  filters: opt(v.array(ListFilterSchema)),
  default_filters: opt(v.record(v.string(), v.unknown())),
});
export type TimelineViewDeclaration = v.InferOutput<typeof TimelineViewDeclarationSchema>;
