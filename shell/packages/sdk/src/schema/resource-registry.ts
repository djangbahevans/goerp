import { cacheUntilRejected } from "./cached-promise.js";
import type { SchemaRegistry } from "./schema-registry.js";
import type { CRUDAction, MetaSchema, RouteSchema } from "./types.js";

// shell-architecture.md §9's ResourceRegistryEntry — a resource's
// resolved CRUD paths, built from route data alone (model + crud_action),
// never guessed from URL shape.
export interface ResourceRegistryEntry {
  module: string;
  resource: string;
  listPath: string;
  getPath: string;
  createPath: string;
  updatePath: string;
  deletePath: string | null;
  listMethod: string;
  createMethod: string;
  updateMethod: string;
  deleteMethod: string | null;
}

// buildResourceRegistry scans every module's routes for entries with
// both `model` and `crud_action` set, and resolves each model to its
// list/get/create/update/delete paths — shell-architecture.md §9's
// buildResourceRegistry, extended to run across every module at once
// (global, keyed by model name) rather than one module's schema at a
// time, since a model name like "contacts.contact" is already
// module-qualified and unique across the whole schema.
export function buildResourceRegistry(schema: MetaSchema): Map<string, ResourceRegistryEntry> {
  const registry = new Map<string, ResourceRegistryEntry>();

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    const byModel = new Map<string, Partial<Record<CRUDAction, RouteSchema>>>();
    for (const route of moduleSchema.routes) {
      if (!route.model || !route.crud_action) continue;
      const crudRoutes = byModel.get(route.model) ?? {};
      crudRoutes[route.crud_action] = route;
      byModel.set(route.model, crudRoutes);
    }

    for (const [modelName, crudRoutes] of byModel) {
      const { list, get, create, update, delete: del } = crudRoutes;
      if (!list && !get) continue; // no usable routes — skip

      registry.set(modelName, {
        module: moduleName,
        resource: modelName,
        listPath: list?.path ?? "",
        getPath: get?.path ?? "",
        createPath: create?.path ?? "",
        updatePath: update?.path ?? "",
        deletePath: del?.path ?? null,
        listMethod: list?.method ?? "GET",
        createMethod: create?.method ?? "POST",
        updateMethod: update?.method ?? "PUT",
        deleteMethod: del?.method ?? null,
      });
    }
  }

  return registry;
}

// Fetches and caches the resource registry for the process lifetime,
// sharing schemaRegistry's own cached schema fetch.
export class ResourceRegistry {
  private readonly getRegistry: () => Promise<Map<string, ResourceRegistryEntry>>;

  constructor(schema: Pick<SchemaRegistry, "getSchema">) {
    this.getRegistry = cacheUntilRejected(() => schema.getSchema().then((s) => buildResourceRegistry(s)));
  }

  async resolve(resource: string): Promise<ResourceRegistryEntry> {
    const registry = await this.getRegistry();
    const entry = registry.get(resource);
    if (!entry) {
      throw new Error(`ResourceRegistry: unknown resource "${resource}"`);
    }
    return entry;
  }
}
