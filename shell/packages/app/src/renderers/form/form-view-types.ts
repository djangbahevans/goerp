import type { FilterOption, ListAction, ListColumn } from "../list/list-view-types.js";

// manifest-spec.md §9.2's Form View wire schema, wire (snake_case) casing.
// `condition`/`readonly_condition` are typed but unevaluated (backlog #18).

// manifest-spec.md §10's Field Types table.
export type FieldType =
  | "text"
  | "textarea"
  | "rich_text"
  | "email"
  | "phone"
  | "url"
  | "number"
  | "integer"
  | "currency"
  | "percent"
  | "date"
  | "datetime"
  | "time"
  | "date_range"
  | "duration"
  | "boolean"
  | "toggle"
  | "select"
  | "multi_select"
  | "radio"
  | "relation"
  | "many2many"
  | "tags"
  | "user_select"
  | "country_select"
  | "language_select"
  | "timezone_select"
  | "currency_select"
  | "color_picker"
  | "icon_picker"
  | "file"
  | "file_multi"
  | "image"
  | "avatar_upload"
  | "signature"
  | "barcode"
  | "qr_code"
  | "rating"
  | "slider"
  | "json"
  | "code"
  | "markdown"
  | "address"
  | "location"
  | "separator"
  | "label"
  | "computed_display"
  | "custom";

// manifest-spec.md's FieldOption object — identical shape to ListFilter's
// own FilterOption, reused rather than redefined.
export type FieldOption = FilterOption;

export interface FormField {
  field: string;
  label?: string;
  type?: FieldType;
  required?: boolean;
  readonly?: boolean;
  readonly_condition?: string;
  hidden?: boolean;
  condition?: string;
  placeholder?: string;
  help_text?: string;
  span?: number;
  autofocus?: boolean;
  computed?: boolean;
  options?: FieldOption[];
  resource?: string;
  resource_filter?: Record<string, unknown>;
  resource_label_field?: string;
  multiple?: boolean;
  creatable?: boolean;
  min?: number;
  max?: number;
  step?: number;
  rows?: number;
  accept?: string;
  max_file_size_mb?: number;
  currency_field?: string;
  align?: "left" | "center" | "right";
  component?: string;
  component_props?: Record<string, unknown>;
  format?: string;
  suffix?: string;
  prefix?: string;
  copy_to_clipboard?: boolean;
  open_in_new_tab?: boolean;
  // "date_range"/"address" bind to more than one field on the record.
  range_start_field?: string;
  range_end_field?: string;
  address_fields?: Record<string, string>;
  // "computed_display" only.
  expression?: string;
  // "code" only.
  language?: string;
  // "label" only.
  label_text?: string;
}

export type FormSectionType = "fields" | "header" | "sub_list" | "custom";

export interface FormSection {
  name?: string;
  label?: string;
  type?: FormSectionType;
  // Layout column count for "fields"/"header"; ListColumn[] for "sub_list".
  columns?: 1 | 2 | 3 | 4 | ListColumn[];
  collapsible?: boolean;
  collapsed_by_default?: boolean;
  condition?: string;
  // "fields"/"header" sections.
  fields?: FormField[];
  // "sub_list" sections.
  field?: string;
  inline_key?: string;
  inline_edit?: boolean;
  add_label?: string;
  max_rows?: number;
  sort?: string;
  create_route?: string;
  update_route?: string;
  delete_route?: string;
  // "custom" sections.
  component?: string;
}

export type FormTabType = "sub_list" | "view" | "fields" | "component";

export interface FormTab {
  label: string;
  icon?: string;
  type: FormTabType;
  permission?: string;
  condition?: string;
  badge_count_route?: string;
  // "sub_list" tabs.
  field?: string;
  inline_key?: string;
  columns?: ListColumn[];
  // "view" tabs.
  view?: string;
  filter?: Record<string, unknown>;
  show_create_action?: boolean;
  // "fields" tabs.
  sections?: FormSection[];
  // "component" tabs.
  component?: string;
}

export interface FormSidebarSection {
  label?: string;
  fields: string[];
}

export interface FormSidebar {
  width?: number;
  sections: FormSidebarSection[];
}

export interface FormViewDeclaration {
  name: string;
  type: "form";
  resource: string;
  label: string;
  icon?: string;
  permission?: string;
  create_route?: string;
  update_route?: string;
  delete_route?: string;
  fetch_route?: string;
  sections: FormSection[];
  tabs?: FormTab[];
  header_actions?: ListAction[];
  sidebar?: FormSidebar | null;
  chatter?: boolean;
  autosave?: boolean;
  readonly_condition?: string;
}
