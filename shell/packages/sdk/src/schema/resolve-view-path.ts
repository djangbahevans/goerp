import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema } from "./types.js";

export const CREATE_PATH_SUFFIX = "/new";

// Create forms use a separate /new browser route because their collection path
// also serves the list view.
export function resolveViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const routes = viewRoutes(schema, viewRef, currentModule);
  const create = routes.find((r) => r.crud_action === "create");
  if (create) return create.path + CREATE_PATH_SUFFIX;
  return routes.find((r) => r.method === "GET")?.path ?? null;
}

// A form may also serve create and update routes; only a GET record route
// can open an existing record in the browser.
export function resolveRecordViewPath(schema: MetaSchema, viewRef: string, currentModule: string): string | null {
  const withId = viewRoutes(schema, viewRef, currentModule).filter((r) => r.path.includes("{id}"));
  const route = withId.find((r) => r.method === "GET");
  return route?.path ?? null;
}

function viewRoutes(schema: MetaSchema, viewRef: string, currentModule: string) {
  const dotIndex = viewRef.indexOf(".");
  const moduleName = dotIndex < 0 ? currentModule : viewRef.slice(0, dotIndex);
  const viewName = dotIndex < 0 ? viewRef : viewRef.slice(dotIndex + 1);
  return schema.modules[moduleName]?.routes.filter((r) => r.view === viewName) ?? [];
}

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
