import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema } from "./types.js";
import { CREATE_PATH_SUFFIX } from "./view-registry.js";

// Resolves a manifest Action's `view` reference ({view_name} same-module,
// or {module}.{view_name}) to the API path that opens it, as the view
// registry resolves it: a view served by a create route opens empty at that
// route's path plus "/new"; any other view at its GET route's path.
export function resolveViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const routes = viewRoutes(schema, viewRef, currentModule);
  const create = routes.find((r) => r.crud_action === "create");
  if (create) return create.path + CREATE_PATH_SUFFIX;
  return routes.find((r) => r.method === "GET")?.path ?? null;
}

// The path that opens one record in viewRef, e.g. "/contacts/{id}": the
// view's GET route with an {id} parameter. A form view is typically also
// served by its create route ("POST /contacts"), which resolveViewPath can
// return first and which names no record.
export function resolveRecordViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const withId = viewRoutes(schema, viewRef, currentModule).filter((r) => r.path.includes("{id}"));
  const route = withId.find((r) => r.method === "GET") ?? withId[0];
  return route?.path ?? null;
}

function viewRoutes(schema: MetaSchema, viewRef: string, currentModule: string) {
  const dotIndex = viewRef.indexOf(".");
  const moduleName = dotIndex < 0 ? currentModule : viewRef.slice(0, dotIndex);
  const viewName = dotIndex < 0 ? viewRef : viewRef.slice(dotIndex + 1);
  return schema.modules[moduleName]?.routes.filter((r) => r.view === viewName) ?? [];
}

// Shares schemaRegistry's own cached schema fetch — same pattern as ActionRegistry.
export class ViewPathRegistry {
  constructor(private readonly schema: Pick<SchemaRegistry, "getSchema">) {}

  async resolve(viewRef: string, currentModule: string): Promise<string | null> {
    const schema = await this.schema.getSchema();
    return resolveViewPath(schema, viewRef, currentModule);
  }

  async resolveRecord(viewRef: string, currentModule: string): Promise<string | null> {
    const schema = await this.schema.getSchema();
    return resolveRecordViewPath(schema, viewRef, currentModule);
  }
}
