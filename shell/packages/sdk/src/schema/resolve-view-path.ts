import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema } from "./types.js";

// Resolves a manifest Action's `view` reference ({view_name} same-module,
// or {module}.{view_name}) to the API path the route serving that view
// declares — a route-scan lookup on RouteSchema.view, the same pattern
// buildResourceRegistry uses for model/crud_action. Not the full
// ViewRegistry (nav tree/model registry/capability filtering) goerp#636
// deferred to backlog #674 — just enough to make a "create" action navigate.
export function resolveViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const route = viewRoutes(schema, viewRef, currentModule)[0];
  return route?.path ?? null;
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
