import { apiClient } from "../http/index.js";
import { ResourceRegistry } from "./resource-registry.js";
import { SchemaRegistry } from "./schema-registry.js";

export type { ResourceRegistryEntry } from "./resource-registry.js";
export { ResourceRegistry } from "./resource-registry.js";
export { SchemaRegistry } from "./schema-registry.js";
export type { CRUDAction, MetaSchema, ModuleSchema, RouteSchema } from "./types.js";

// Shared singletons — one schema fetch, agreed on by every consumer.
export const schemaRegistry = new SchemaRegistry(apiClient);
export const resourceRegistry = new ResourceRegistry(schemaRegistry);
