// The minimal `ListViewDeclaration` shape ListRenderer itself needs —
// mode switching, URL/local state, field security. Grounded in
// internal/engine/manifest/manifest.go's View/ListColumn structs (the
// real /_meta/schema wire shape, goerp repo), kept in wire (snake_case)
// casing to match RouteSchema/ModuleSchema's own convention.
//
// The full column-type union, filter-type catalog, and action rendering
// are goerp#575's own job (List view manifest schema and column types) —
// this type carries just enough of each shape for ListRenderer to
// resolve a resource, apply field security, and pass columns/filters/
// actions through unmodified to whatever #575 renders them with.
export interface ListColumn {
  field: string;
  label?: string;
  type?: string;
  sortable?: boolean;
  primary?: boolean;
}

export interface ListFilter {
  field: string;
  label?: string;
  type?: string;
  default?: unknown;
}

export interface ListAction {
  label: string;
  type: string;
  view?: string;
  permission?: string;
}

export interface ListViewDeclaration {
  name: string;
  type: "list";
  resource: string;
  label: string;
  permission?: string;
  columns?: ListColumn[];
  default_sort?: string;
  default_sort_dir?: "asc" | "desc";
  filters?: ListFilter[];
  actions?: ListAction[];
  group_by_options?: string[];
}
