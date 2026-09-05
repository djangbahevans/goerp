import type { SchemaRegistry } from "../schema/index.js";
import { schemaRegistry } from "../schema/index.js";

export interface ActionRoute {
  method: string;
  path: string;
}

// Resolves a useAction() route name ({module}.{name}) against
// GET /_meta/schema, per shell-architecture.md's "How named action
// routes are resolved". Shares schemaRegistry's own cached schema fetch
// rather than fetching independently.
export class ActionRegistry {
  constructor(private readonly schema: Pick<SchemaRegistry, "getSchema">) {}

  async resolve(routeName: string): Promise<ActionRoute> {
    const dotIndex = routeName.indexOf(".");
    if (dotIndex < 0) {
      throw new Error(`useAction: route "${routeName}" must be in "{module}.{name}" format`);
    }
    const moduleName = routeName.slice(0, dotIndex);
    const actionName = routeName.slice(dotIndex + 1);

    const schema = await this.schema.getSchema();
    const moduleSchema = schema.modules[moduleName];
    if (!moduleSchema) {
      throw new Error(`useAction: unknown module "${moduleName}" in route "${routeName}"`);
    }
    const route = moduleSchema.routes.find((r) => r.name === actionName);
    if (!route) {
      throw new Error(`useAction: no action named "${actionName}" in module "${moduleName}"`);
    }
    return { method: route.method, path: route.path };
  }
}

export const actionRegistry = new ActionRegistry(schemaRegistry);
