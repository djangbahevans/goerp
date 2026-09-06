import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema } from "./types.js";

// Resolves a manifest Action's `view` reference ({view_name} same-module,
// or {module}.{view_name}) to the API path the route serving that view
// declares — a route-scan lookup on RouteSchema.view, the same pattern
// buildResourceRegistry uses for model/crud_action. Not the full
// ViewRegistry (nav tree/model registry/capability filtering) goerp#636
// deferred to backlog #674 — just enough to make a "create" action navigate.
export function resolveViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const dotIndex = viewRef.indexOf(".");
  const moduleName = dotIndex < 0 ? currentModule : viewRef.slice(0, dotIndex);
  const viewName = dotIndex < 0 ? viewRef : viewRef.slice(dotIndex + 1);

  const moduleSchema = schema.modules[moduleName];
  if (!moduleSchema) return null;

  const route = moduleSchema.routes.find((r) => r.view === viewName);
  return route?.path ?? null;
}

// Shares schemaRegistry's own cached schema fetch — same pattern as ActionRegistry.
export class ViewPathRegistry {
  constructor(private readonly schema: Pick<SchemaRegistry, "getSchema">) {}

  async resolve(viewRef: string, currentModule: string): Promise<string | null> {
    const schema = await this.schema.getSchema();
    return resolveViewPath(schema, viewRef, currentModule);
  }
}
