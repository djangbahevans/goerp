import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";
import { type FilterOption, FilterOptionSchema, ListActionSchema, ListColumnSchema } from "../list/list-view-types.js";

// manifest-spec.md §9.2's Form View wire schema, as valibot schemas —
// goerp#839's own runtime counterpart to the plain TS interfaces this file
// used to only have (unlike every sibling *-view-types.ts file). Wiring
// "form" into view-dispatch.tsx's KNOWN_VIEW_TYPES/AnyViewDeclarationSchema
// and the dispatch switch itself is goerp#840's job, not this file's.
// `condition`/`readonly_condition` are typed but unevaluated (backlog #18).

// manifest-spec.md §10's Field Types table.
const FIELD_TYPES = [
  "text",
  "textarea",
  "rich_text",
  "email",
  "phone",
  "url",
  "number",
  "integer",
  "currency",
  "percent",
  "date",
  "datetime",
  "time",
  "date_range",
  "duration",
  "boolean",
  "toggle",
  "select",
  "multi_select",
  "radio",
  "relation",
  "many2many",
  "tags",
  "user_select",
  "country_select",
  "language_select",
  "timezone_select",
  "currency_select",
  "color_picker",
  "icon_picker",
  "file",
  "file_multi",
  "image",
  "avatar_upload",
  "signature",
  "barcode",
  "qr_code",
  "rating",
  "slider",
  "json",
  "code",
  "markdown",
  "address",
  "location",
  "separator",
  "label",
  "computed_display",
  "custom",
] as const;
export type FieldType = (typeof FIELD_TYPES)[number];

// manifest-spec.md's FieldOption object — identical shape to ListFilter's
// own FilterOption, reused rather than redefined.
export type FieldOption = FilterOption;

export const FormFieldSchema = v.looseObject({
  field: v.string(),
  label: opt(v.string()),
  type: opt(v.picklist(FIELD_TYPES)),
  required: opt(v.boolean()),
  readonly: opt(v.boolean()),
  readonly_condition: opt(v.string()),
  hidden: opt(v.boolean()),
  condition: opt(v.string()),
  placeholder: opt(v.string()),
  help_text: opt(v.string()),
  span: opt(v.number()),
  autofocus: opt(v.boolean()),
  computed: opt(v.boolean()),
  options: opt(v.array(FilterOptionSchema)),
  resource: opt(v.string()),
  resource_filter: opt(v.record(v.string(), v.unknown())),
  resource_label_field: opt(v.string()),
  multiple: opt(v.boolean()),
  creatable: opt(v.boolean()),
  min: opt(v.number()),
  max: opt(v.number()),
  step: opt(v.number()),
  rows: opt(v.number()),
  accept: opt(v.string()),
  max_file_size_mb: opt(v.number()),
  currency_field: opt(v.string()),
  align: opt(v.picklist(["left", "center", "right"] as const)),
  component: opt(v.string()),
  component_props: opt(v.record(v.string(), v.unknown())),
  format: opt(v.string()),
  suffix: opt(v.string()),
  prefix: opt(v.string()),
  copy_to_clipboard: opt(v.boolean()),
  open_in_new_tab: opt(v.boolean()),
  // "date_range" only — binds to two fields instead of one.
  range_start_field: opt(v.string()),
  range_end_field: opt(v.string()),
  // "address" only — binds to multiple fields via a field-name mapping.
  address_fields: opt(v.record(v.string(), v.string())),
  // "computed_display" only.
  expression: opt(v.string()),
  // "code" only.
  language: opt(v.string()),
  // "label" only.
  label_text: opt(v.string()),
  // "barcode" only. barcode-field.md's on_scan_route orchestration and its
  // own proposed default symbology set, overridable per field.
  on_scan_route: opt(v.string()),
  formats: opt(v.array(v.string())),
});
export type FormField = v.InferOutput<typeof FormFieldSchema>;

const FORM_SECTION_TYPES = ["fields", "header", "sub_list", "custom"] as const;
export type FormSectionType = (typeof FORM_SECTION_TYPES)[number];

export const FormSectionSchema = v.looseObject({
  name: opt(v.string()),
  label: opt(v.string()),
  type: opt(v.picklist(FORM_SECTION_TYPES)),
  // Layout column count for "fields"/"header"; ListColumn[] for "sub_list".
  columns: opt(v.union([v.literal(1), v.literal(2), v.literal(3), v.literal(4), v.array(ListColumnSchema)])),
  collapsible: opt(v.boolean()),
  collapsed_by_default: opt(v.boolean()),
  condition: opt(v.string()),
  // "fields"/"header" sections.
  fields: opt(v.array(FormFieldSchema)),
  // "sub_list" sections.
  field: opt(v.string()),
  inline_key: opt(v.string()),
  inline_edit: opt(v.boolean()),
  add_label: opt(v.string()),
  max_rows: opt(v.number()),
  sort: opt(v.string()),
  create_route: opt(v.string()),
  update_route: opt(v.string()),
  delete_route: opt(v.string()),
  // "custom" sections.
  component: opt(v.string()),
});
export type FormSection = v.InferOutput<typeof FormSectionSchema>;

const FORM_TAB_TYPES = ["sub_list", "view", "fields", "component"] as const;
export type FormTabType = (typeof FORM_TAB_TYPES)[number];

export const FormTabSchema = v.looseObject({
  label: v.string(),
  icon: opt(v.string()),
  type: v.picklist(FORM_TAB_TYPES),
  permission: opt(v.string()),
  condition: opt(v.string()),
  badge_count_route: opt(v.string()),
  // "sub_list" tabs.
  field: opt(v.string()),
  inline_key: opt(v.string()),
  columns: opt(v.array(ListColumnSchema)),
  // "view" tabs.
  view: opt(v.string()),
  filter: opt(v.record(v.string(), v.unknown())),
  show_create_action: opt(v.boolean()),
  // "fields" tabs.
  sections: opt(v.array(FormSectionSchema)),
  // "component" tabs.
  component: opt(v.string()),
});
export type FormTab = v.InferOutput<typeof FormTabSchema>;

export const FormSidebarSectionSchema = v.looseObject({
  label: opt(v.string()),
  fields: v.array(v.string()),
});
export type FormSidebarSection = v.InferOutput<typeof FormSidebarSectionSchema>;

export const FormSidebarSchema = v.looseObject({
  width: opt(v.number()),
  sections: v.array(FormSidebarSectionSchema),
});
export type FormSidebar = v.InferOutput<typeof FormSidebarSchema>;

export const FormViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("form"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  create_route: opt(v.string()),
  update_route: opt(v.string()),
  delete_route: opt(v.string()),
  fetch_route: opt(v.string()),
  sections: v.array(FormSectionSchema),
  tabs: opt(v.array(FormTabSchema)),
  header_actions: opt(v.array(ListActionSchema)),
  sidebar: opt(FormSidebarSchema),
  chatter: opt(v.boolean()),
  autosave: opt(v.boolean()),
  readonly_condition: opt(v.string()),
});
export type FormViewDeclaration = v.InferOutput<typeof FormViewDeclarationSchema>;
