// A fetched record — the shape column rendering and grouping work against.
export type Row = Record<string, unknown>;

// manifest-spec.md §9.1's List View wire schema — the canonical shape,
// kept in wire (snake_case) casing to match RouteSchema/ModuleSchema.
// `condition` fields are typed and passed through but never evaluated —
// the shell-side domain-expression interpreter they need is unfiled
// (backlog #18's own note: needs its own triage pass). `ConfirmDialog`
// is omitted from ListAction — goerp#575's own out-of-scope note.
export type ColumnType =
  | "text"
  | "number"
  | "currency"
  | "percent"
  | "date"
  | "datetime"
  | "time"
  | "relative_time"
  | "boolean"
  | "badge"
  | "avatar"
  | "email"
  | "phone"
  | "url"
  | "country"
  | "tags"
  | "relation"
  | "file"
  | "color"
  | "json"
  | "custom";

export interface BadgeValue {
  label: string;
  color?: string;
  icon?: string;
}

export type BadgeConfig = Record<string, BadgeValue>;

export interface ListColumn {
  field: string;
  label?: string;
  type?: ColumnType;
  sortable?: boolean;
  width?: number;
  min_width?: number;
  max_width?: number;
  truncate?: boolean;
  align?: "left" | "center" | "right";
  primary?: boolean;
  hidden?: boolean;
  href?: string;
  condition?: string;
  badge_config?: BadgeConfig;
  avatar_field?: string;
  format?: string;
  resource?: string;
  display_field?: string;
  resource_label_field?: string;
  currency_field?: string;
}

export type FilterType =
  | "text"
  | "select"
  | "multi_select"
  | "radio"
  | "boolean"
  | "date"
  | "daterange"
  | "number"
  | "number_range"
  | "relation"
  | "tags"
  | "user_select"
  | "country_select";

export interface FilterOption {
  value: string;
  label: string;
  icon?: string;
  color?: string;
  disabled?: boolean;
}

export interface ListFilter {
  field: string;
  label: string;
  type: FilterType;
  options?: FilterOption[];
  default?: unknown;
  multiple?: boolean;
  resource?: string;
  resource_filter?: Record<string, unknown>;
  condition?: string;
  search_params?: string;
}

export type ActionType = "create" | "route" | "export" | "import" | "report" | "url" | "custom";

export interface ListAction {
  label: string;
  type: ActionType;
  view?: string;
  icon?: string;
  style?: "primary" | "secondary" | "ghost" | "danger";
  permission?: string;
  condition?: string;
  route?: string;
  route_params?: Record<string, unknown>;
  report?: string;
  url?: string;
  component?: string;
}

export interface EmptyStateAction {
  label: string;
  type: string;
  view?: string;
}

export interface EmptyState {
  title?: string;
  description?: string;
  icon?: string;
  action?: EmptyStateAction;
}

export interface ListViewDeclaration {
  name: string;
  type: "list";
  resource: string;
  label: string;
  icon?: string;
  permission?: string;
  columns?: ListColumn[];
  default_sort?: string;
  default_sort_dir?: "asc" | "desc";
  // Parsed, never applied — goerp#593's own filter-initialization scope.
  default_filters?: Record<string, unknown>;
  label_field?: string;
  row_click?: string;
  row_click_param?: string;
  selectable?: boolean;
  filters?: ListFilter[];
  actions?: ListAction[];
  // goerp#595's own scope — carried on the type, not rendered here.
  bulk_actions?: unknown[];
  group_by_options?: string[];
  page_sizes?: number[];
  default_page_size?: number;
  density?: "compact" | "normal" | "comfortable";
  empty_state?: EmptyState;
}
