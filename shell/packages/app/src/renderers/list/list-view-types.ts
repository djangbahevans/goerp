import { AlertDialogInputSchema } from "@goerp/sdk/components";
import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";

// A fetched record — the shape column rendering and grouping work against.
export type Row = Record<string, unknown>;

// manifest-spec.md §9.1's List View wire schema, as valibot schemas.
// `condition` is passed through but never evaluated (backlog #18) — an
// action with one is always shown/enabled, everywhere ListAction is used.

const COLUMN_TYPES = [
  "text",
  "number",
  "currency",
  "percent",
  "date",
  "datetime",
  "time",
  "relative_time",
  "boolean",
  "badge",
  "avatar",
  "email",
  "phone",
  "url",
  "country",
  "tags",
  "relation",
  "file",
  "color",
  "json",
  "custom",
] as const;
export type ColumnType = (typeof COLUMN_TYPES)[number];

const BUTTON_STYLES = ["primary", "secondary", "ghost", "danger"] as const;

export const BadgeValueSchema = v.looseObject({
  label: v.string(),
  color: opt(v.string()),
  icon: opt(v.string()),
});
export type BadgeValue = v.InferOutput<typeof BadgeValueSchema>;

export const BadgeConfigSchema = v.record(v.string(), BadgeValueSchema);
export type BadgeConfig = v.InferOutput<typeof BadgeConfigSchema>;

export const ListColumnSchema = v.looseObject({
  field: v.string(),
  label: opt(v.string()),
  type: opt(v.picklist(COLUMN_TYPES)),
  sortable: opt(v.boolean()),
  width: opt(v.number()),
  min_width: opt(v.number()),
  max_width: opt(v.number()),
  truncate: opt(v.boolean()),
  align: opt(v.picklist(["left", "center", "right"] as const)),
  primary: opt(v.boolean()),
  hidden: opt(v.boolean()),
  href: opt(v.string()),
  condition: opt(v.string()),
  badge_config: opt(BadgeConfigSchema),
  avatar_field: opt(v.string()),
  format: opt(v.string()),
  resource: opt(v.string()),
  display_field: opt(v.string()),
  resource_label_field: opt(v.string()),
  currency_field: opt(v.string()),
  component: opt(v.string()),
  component_props: opt(v.record(v.string(), v.unknown())),
});
export type ListColumn = v.InferOutput<typeof ListColumnSchema>;

const FILTER_TYPES = [
  "text",
  "select",
  "multi_select",
  "radio",
  "boolean",
  "date",
  "daterange",
  "number",
  "number_range",
  "relation",
  "tags",
  "user_select",
  "country_select",
] as const;
export type FilterType = (typeof FILTER_TYPES)[number];

export const FilterOptionSchema = v.looseObject({
  value: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  color: opt(v.string()),
  disabled: opt(v.boolean()),
});
export type FilterOption = v.InferOutput<typeof FilterOptionSchema>;

export const ListFilterSchema = v.looseObject({
  field: v.string(),
  label: v.string(),
  type: v.picklist(FILTER_TYPES),
  options: opt(v.array(FilterOptionSchema)),
  default: opt(v.unknown()),
  multiple: opt(v.boolean()),
  resource: opt(v.string()),
  resource_filter: opt(v.record(v.string(), v.unknown())),
  condition: opt(v.string()),
  search_params: opt(v.string()),
});
export type ListFilter = v.InferOutput<typeof ListFilterSchema>;

// "separator" only makes sense inside a "menu" action's own `items` —
// every other Action consumer treats it the same as an unhandled type
// (falls through to their existing default: return null).
const ACTION_TYPES = ["create", "route", "export", "import", "report", "url", "custom", "menu", "separator"] as const;
export type ActionType = (typeof ACTION_TYPES)[number];

// manifest-spec.md's ConfirmInput object, plus the `field` key AlertDialog itself doesn't need.
export const ActionConfirmInputSchema = v.looseObject({
  ...AlertDialogInputSchema.entries,
  field: v.string(),
});
export type ActionConfirmInput = v.InferOutput<typeof ActionConfirmInputSchema>;

// manifest-spec.md's ConfirmDialog object — shared by ListAction (header
// actions, row actions) and BulkAction, not bulk-actions-specific despite
// this file's older Bulk-prefixed naming having briefly suggested otherwise.
export const ActionConfirmSchema = v.looseObject({
  title: v.string(),
  message: v.string(),
  confirm_label: opt(v.string()),
  cancel_label: opt(v.string()),
  destructive: opt(v.boolean()),
  input: opt(ActionConfirmInputSchema),
});
export type ActionConfirm = v.InferOutput<typeof ActionConfirmSchema>;

// manifest-spec.md's Action object. Recursive: a "menu"-type action's
// `items` are themselves Actions, down to a "separator" entry (the only
// type allowed no `label`, enforced by the check below). v.lazy needs the
// output type spelled out by hand (ListAction below) rather than inferred
// via v.InferOutput, since the schema can't reference its own
// not-yet-inferred type while being defined.
export interface ListAction {
  label?: string | undefined;
  type: ActionType;
  view?: string | undefined;
  icon?: string | undefined;
  style?: (typeof BUTTON_STYLES)[number] | undefined;
  permission?: string | undefined;
  condition?: string | undefined;
  route?: string | undefined;
  route_params?: Record<string, unknown> | undefined;
  report?: string | undefined;
  // For type: "report" — the downloaded file's extension. Default: "pdf".
  format?: string | undefined;
  url?: string | undefined;
  component?: string | undefined;
  confirm?: ActionConfirm | undefined;
  // Only meaningful for type: "menu" — the dropdown's own entries.
  items?: ListAction[] | undefined;
}

export const ListActionSchema: v.GenericSchema<unknown, ListAction> = v.pipe(
  v.looseObject({
    label: opt(v.string()),
    type: v.picklist(ACTION_TYPES),
    view: opt(v.string()),
    icon: opt(v.string()),
    style: opt(v.picklist(BUTTON_STYLES)),
    permission: opt(v.string()),
    condition: opt(v.string()),
    route: opt(v.string()),
    route_params: opt(v.record(v.string(), v.unknown())),
    report: opt(v.string()),
    format: opt(v.string()),
    url: opt(v.string()),
    component: opt(v.string()),
    confirm: opt(ActionConfirmSchema),
    items: opt(v.array(v.lazy((): v.GenericSchema<unknown, ListAction> => ListActionSchema))),
  }),
  // label is only truly optional for "separator" (manifest-spec.md's Action object).
  v.check(
    (value) => value.type === "separator" || Boolean(value.label),
    'label is required unless type is "separator"',
  ),
);

// manifest-spec.md's BulkAction object — Action's fields plus `confirm`/min_selected/max_selected.
export const BulkActionSchema = v.looseObject({
  label: v.string(),
  type: v.picklist(ACTION_TYPES),
  icon: opt(v.string()),
  style: opt(v.picklist(BUTTON_STYLES)),
  permission: opt(v.string()),
  condition: opt(v.string()),
  route: opt(v.string()),
  route_params: opt(v.record(v.string(), v.unknown())),
  format: opt(v.string()),
  component: opt(v.string()),
  confirm: opt(ActionConfirmSchema),
  // Default: 1.
  min_selected: opt(v.number()),
  max_selected: opt(v.number()),
});
export type BulkAction = v.InferOutput<typeof BulkActionSchema>;

export const EmptyStateActionSchema = v.looseObject({
  label: v.string(),
  type: v.string(),
  view: opt(v.string()),
});
export type EmptyStateAction = v.InferOutput<typeof EmptyStateActionSchema>;

export const EmptyStateSchema = v.looseObject({
  title: opt(v.string()),
  description: opt(v.string()),
  icon: opt(v.string()),
  action: opt(EmptyStateActionSchema),
});
export type EmptyState = v.InferOutput<typeof EmptyStateSchema>;

export const ListViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("list"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  columns: opt(v.array(ListColumnSchema)),
  default_sort: opt(v.string()),
  default_sort_dir: opt(v.picklist(["asc", "desc"] as const)),
  // Parsed, never applied — goerp#593's own filter-initialization scope.
  default_filters: opt(v.record(v.string(), v.unknown())),
  label_field: opt(v.string()),
  row_click: opt(v.string()),
  row_click_param: opt(v.string()),
  selectable: opt(v.boolean()),
  filters: opt(v.array(ListFilterSchema)),
  actions: opt(v.array(ListActionSchema)),
  bulk_actions: opt(v.array(BulkActionSchema)),
  group_by_options: opt(v.array(v.string())),
  // view-system.md §4 "Hierarchical lists (tree_field)". Mutually exclusive
  // with group_by_options in practice — ListRenderer renders a tree_field
  // view as a single hierarchy, never combined with a group-by bucketing of
  // the same rows.
  tree_field: opt(v.string()),
  default_expanded_depth: opt(v.number()),
  page_sizes: opt(v.array(v.number())),
  default_page_size: opt(v.number()),
  density: opt(v.picklist(["compact", "normal", "comfortable"] as const)),
  empty_state: opt(EmptyStateSchema),
});
export type ListViewDeclaration = v.InferOutput<typeof ListViewDeclarationSchema>;
