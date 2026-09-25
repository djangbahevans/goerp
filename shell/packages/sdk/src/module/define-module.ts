import { componentRegistry } from "../schema/index.js";
import { extensionBatchLoaderRegistry } from "./extension-batch-loader-registry.js";
import { moduleApiRegistry } from "./module-api-registry.js";
import { moduleErrorHandlerRegistry } from "./module-error-handler-registry.js";
import { moduleNavigationRegistry } from "./module-navigation-registry.js";
import type { ModuleDefinition } from "./module-types.js";

interface RegisteredKeys {
  views: string[];
  fieldRenderers: string[];
  batchLoaders: string[];
}

// A hot-reloaded module re-registers under the same name with a new
// bundle_url/bundle_sha256 — tracked here so its old keys unregister first
// instead of hitting the collision guard below.
const previouslyRegistered = new Map<string, RegisteredKeys>();

// typescript-sdk-reference.md §3. `commands` isn't registered here — see
// bootstrap/register-module.ts, which reads it off the returned definition.
export function defineModule(definition: ModuleDefinition): ModuleDefinition {
  const previous = previouslyRegistered.get(definition.name);
  for (const name of previous?.views ?? []) componentRegistry.unregister(name);
  for (const key of previous?.fieldRenderers ?? []) componentRegistry.unregister(key);
  for (const key of previous?.batchLoaders ?? []) extensionBatchLoaderRegistry.unregister(key);

  const views = Object.entries(definition.views ?? {});
  const fieldRenderers = Object.entries(definition.fieldRenderers ?? {});
  const batchLoaders = Object.entries(definition.batchLoaders ?? {});
  for (const [name, component] of views) componentRegistry.register(name, component);
  for (const [key, component] of fieldRenderers) componentRegistry.register(key, component);
  for (const [key, loader] of batchLoaders) extensionBatchLoaderRegistry.register(key, loader);
  previouslyRegistered.set(definition.name, {
    views: views.map(([name]) => name),
    fieldRenderers: fieldRenderers.map(([key]) => key),
    batchLoaders: batchLoaders.map(([key]) => key),
  });

  // Registered unconditionally (not just when present) so a hot-reloaded
  // module that stops declaring navigation/errorHandlers/api clears its prior
  // bundle's entry — each registry treats a nullish value as "clear".
  moduleNavigationRegistry.register(definition.name, definition.navigation);
  moduleErrorHandlerRegistry.register(definition.name, definition.errorHandlers);
  moduleApiRegistry.register(definition.name, definition.api);
  return definition;
}
