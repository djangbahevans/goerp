import { apiClient } from "../http/index.js";
import { BatchLoaderRegistry } from "./batch-loader-registry.js";
import { ComponentRegistry } from "./component-registry.js";
import { ModelRegistry } from "./model-registry.js";
import { ViewDeclarationRegistry } from "./resolve-view-declaration.js";
import { ViewPathRegistry } from "./resolve-view-path.js";
import { ResourceMetadataRegistry } from "./resource-metadata-registry.js";
import { ResourceRegistry } from "./resource-registry.js";
import { SchemaRegistry } from "./schema-registry.js";

export type { BatchLoader } from "./batch-loader-registry.js";
export { BatchLoaderRegistry } from "./batch-loader-registry.js";
export type { RegisteredComponent } from "./component-registry.js";
export { ComponentRegistry } from "./component-registry.js";
export { buildModelRegistry, ModelRegistry } from "./model-registry.js";
export type { ViewDeclaration } from "./resolve-view-declaration.js";
export { resolveViewDeclaration, ViewDeclarationRegistry } from "./resolve-view-declaration.js";
export { resolveViewPath, ViewPathRegistry } from "./resolve-view-path.js";
export type { ResourceMetadataEntry } from "./resource-metadata-registry.js";
export {
  buildResourceMetadataRegistry,
  ResourceMetadataRegistry,
  resourceListPath,
} from "./resource-metadata-registry.js";
export type { ResourceRegistryEntry } from "./resource-registry.js";
export { ResourceRegistry } from "./resource-registry.js";
export { SchemaRegistry } from "./schema-registry.js";
export type { CRUDAction, FieldDef, MetaSchema, ModelDef, ModuleSchema, RouteSchema } from "./types.js";

// Shared singletons — one schema fetch, agreed on by every consumer.
export const schemaRegistry = new SchemaRegistry(apiClient);
export const resourceRegistry = new ResourceRegistry(schemaRegistry);
export const resourceMetadataRegistry = new ResourceMetadataRegistry(schemaRegistry);
export const viewPathRegistry = new ViewPathRegistry(schemaRegistry);
export const modelRegistry = new ModelRegistry(schemaRegistry);
export const viewDeclarationRegistry = new ViewDeclarationRegistry(schemaRegistry);
export const componentRegistry = new ComponentRegistry();
export const batchLoaderRegistry = new BatchLoaderRegistry();
