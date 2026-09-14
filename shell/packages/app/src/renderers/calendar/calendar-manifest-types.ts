import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";
import { ListFilterSchema } from "../list/list-view-types.js";
import { ALL_CALENDAR_VIEWS } from "./calendar-view-types.js";

// manifest-spec.md §9.4's Calendar View wire schema, resolved by
// calendar-renderer.tsx into CalendarViewProps.
export const CalendarViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("calendar"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  date_field: v.string(),
  end_date_field: opt(v.string()),
  title_field: v.string(),
  color_field: opt(v.string()),
  color_map: opt(v.record(v.string(), v.string())),
  default_view: opt(v.picklist(ALL_CALENDAR_VIEWS)),
  allowed_views: opt(v.array(v.picklist(ALL_CALENDAR_VIEWS))),
  filters: opt(v.array(ListFilterSchema)),
  default_filters: opt(v.record(v.string(), v.unknown())),
  on_click: opt(v.string()),
  on_date_click: opt(v.string()),
  quick_create: opt(v.boolean()),
});
export type CalendarViewDeclaration = v.InferOutput<typeof CalendarViewDeclarationSchema>;
