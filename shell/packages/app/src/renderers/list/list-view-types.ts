// Minimal subset of manifest.go's View/ListColumn structs ListRenderer
// itself needs. The full column-type/filter-type union is goerp#575's job.
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
