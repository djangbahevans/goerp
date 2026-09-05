import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";

// A minimal subset of shell-architecture.md §9's MetaSchema/RouteSchema —
// only the fields action-route resolution needs. The full resource
// registry (routes, views, models, navigation) is a separate, larger
// concern (backlog "Shell resource registry") this does not attempt to
// replace.
interface MetaSchemaRoute {
  method: string;
  path: string;
  name: string | null;
}

interface MetaSchemaModule {
  routes: MetaSchemaRoute[];
}

interface MetaSchema {
  modules: Record<string, MetaSchemaModule>;
}

export interface ActionRoute {
  method: string;
  path: string;
}

// Resolves a useAction() route name ({module}.{name}) against
// GET /_meta/schema, per shell-architecture.md's "How named action
// routes are resolved". Caches the schema fetch for the process
// lifetime — module reload invalidation is the resource registry
// ticket's concern, not this one's.
export class ActionRegistry {
  private schemaPromise: Promise<MetaSchema> | null = null;

  constructor(private readonly client: Pick<APIClient, "get">) {}

  async resolve(routeName: string): Promise<ActionRoute> {
    const dotIndex = routeName.indexOf(".");
    if (dotIndex < 0) {
      throw new Error(`useAction: route "${routeName}" must be in "{module}.{name}" format`);
    }
    const moduleName = routeName.slice(0, dotIndex);
    const actionName = routeName.slice(dotIndex + 1);

    const schema = await this.fetchSchema();
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

  private fetchSchema(): Promise<MetaSchema> {
    this.schemaPromise ??= this.client.get<MetaSchema>("/_meta/schema").catch((err: unknown) => {
      this.schemaPromise = null;
      throw err;
    });
    return this.schemaPromise;
  }
}

export const actionRegistry = new ActionRegistry(apiClient);
