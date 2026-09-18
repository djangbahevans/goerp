import { BatchLoaderRegistry } from "./batch-loader-registry.js";
import { ComponentRegistry } from "./component-registry.js";
import { ModelRegistry } from "./model-registry.js";
import { ViewDeclarationRegistry } from "./resolve-view-declaration.js";
import { ViewPathRegistry } from "./resolve-view-path.js";
import { ResourceMetadataRegistry } from "./resource-metadata-registry.js";
import { ResourceRegistry } from "./resource-registry.js";
import { schemaRegistry } from "./schema-registry.js";

export type { BatchLoader } from "./batch-loader-registry.js";
export { BatchLoaderRegistry } from "./batch-loader-registry.js";
export type { RegisteredComponent } from "./component-registry.js";
export { ComponentRegistry } from "./component-registry.js";
export { buildModelRegistry, ModelRegistry } from "./model-registry.js";
export { optionalNullable } from "./optional-nullable.js";
export type { ParseManifestResult } from "./parse-manifest-value.js";
export { parseManifestValue } from "./parse-manifest-value.js";
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
export { SchemaRegistry, schemaRegistry } from "./schema-registry.js";
export { summarizeIssues } from "./summarize-issues.js";
export type {
  CRUDAction,
  FieldDef,
  FieldWorkflow,
  MetaSchema,
  ModelDef,
  ModuleSchema,
  RouteSchema,
  WorkflowTransition,
} from "./types.js";
export type { NavigationGroup, NavigationItem, ResolvedView, ViewRegistry } from "./view-registry.js";
export { buildEmptyViewRegistry, buildViewRegistry, filterViewByCapability } from "./view-registry.js";
export {
  ViewRegistryContext,
  ViewRegistryProvider,
  ViewRegistryProviderForTenant,
  viewRegistryRef,
} from "./view-registry-provider.js";

// Shared singletons — one schema fetch, agreed on by every consumer.
// schemaRegistry itself is instantiated in schema-registry.ts, not here —
// see that file's own comment on why.
export const resourceRegistry = new ResourceRegistry(schemaRegistry);
export const resourceMetadataRegistry = new ResourceMetadataRegistry(schemaRegistry);
export const viewPathRegistry = new ViewPathRegistry(schemaRegistry);
export const modelRegistry = new ModelRegistry(schemaRegistry);
export const viewDeclarationRegistry = new ViewDeclarationRegistry(schemaRegistry);
export const componentRegistry = new ComponentRegistry();
export const batchLoaderRegistry = new BatchLoaderRegistry();
