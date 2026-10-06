import { cacheUntilRejected } from "./cached-promise.js";
import type { SchemaRegistry } from "./schema-registry.js";
import { type CRUDAction, isCRUDAction, type MetaSchema, type RouteSchema } from "./types.js";

export interface ResourceRegistryEntry {
  module: string;
  resource: string;
  listPath: string;
  getPath: string;
  createPath: string;
  updatePath: string;
  deletePath: string | null;
  pivotPath: string | null;
  listMethod: string;
  createMethod: string;
  updateMethod: string;
  deleteMethod: string | null;
  // The permissions each write route declares, all of which a caller
  // must hold (editable-sub-list.md). null when the route doesn't exist.
  createPermissions: string[] | null;
  updatePermissions: string[] | null;
  deletePermissions: string[] | null;
  // POST /{plural}/preview (go-sdk-reference.md §22 "Preview action"),
  // null when the model doesn't enable it.
  previewPath: string | null;
}

// Only engine-native CRUD routes and reserved-name engine.DefineAction overrides
// serve model ops. A raw engine.Model binding only governs field security.
export function buildResourceRegistry(schema: MetaSchema): Map<string, ResourceRegistryEntry> {
  const registry = new Map<string, ResourceRegistryEntry>();

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    const byModel = new Map<string, Partial<Record<CRUDAction, RouteSchema>>>();
    for (const route of moduleSchema.routes) {
      if (!route.model || !route.crud_action || !isCRUDAction(route.crud_action)) continue;
      if (!route.engine_native && route.name !== route.crud_action) continue;
      const crudRoutes = byModel.get(route.model) ?? {};
      crudRoutes[route.crud_action] = route;
      byModel.set(route.model, crudRoutes);
    }

    for (const [modelName, crudRoutes] of byModel) {
      const { list, get, create, update, delete: del, preview, pivot } = crudRoutes;
      if (!list && !get) continue;

      registry.set(modelName, {
        module: moduleName,
        resource: modelName,
        listPath: list?.path ?? "",
        getPath: get?.path ?? "",
        createPath: create?.path ?? "",
        updatePath: update?.path ?? "",
        deletePath: del?.path ?? null,
        pivotPath: pivot?.path ?? null,
        listMethod: list?.method ?? "GET",
        createMethod: create?.method ?? "POST",
        updateMethod: update?.method ?? "PUT",
        deleteMethod: del?.method ?? null,
        createPermissions: create?.permissions ?? null,
        updatePermissions: update?.permissions ?? null,
        deletePermissions: del?.permissions ?? null,
        previewPath: preview?.path ?? null,
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
