// shell-architecture.md §9's MetaSchema/ModuleSchema/RouteSchema. Go's
// `omitempty` tags mean an absent field is missing from the JSON, not
// null, hence optional properties rather than `| null` here. `views`,
// `navigation`, `models`, `permissions`, and `frontend` stay loosely
// typed — goerp#575/#674's own jobs, not this module's.

export type CRUDAction = "get" | "list" | "create" | "update" | "delete";

export interface RouteSchema {
  method: string;
  path: string;
  permissions: string[];
  model?: string;
  crud_action?: CRUDAction;
  name?: string;
  response_is_list: boolean;
  view?: string;
}

export interface ModuleSchema {
  name: string;
  version: string;
  display_name: string;
  routes: RouteSchema[];
  views: unknown[];
  navigation: unknown[];
  models: Record<string, unknown>;
  permissions: unknown[];
  frontend: { bundle_url: string; bundle_sha256: string } | null;
  public_config: Record<string, unknown>;
}

export interface MetaSchema {
  modules: Record<string, ModuleSchema>;
  engine_version: string;
  schema_hash: string;
}
