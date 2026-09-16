import type { ExtensionBatchLoader } from "./module-types.js";

// defineModule().batchLoaders (view-system.md §4's extension-fields example),
// keyed `{target_module}.{target_view_name}` — distinct from @goerp/sdk/schema's
// BatchLoaderRegistry (per relation column, id-to-label strings).
export class ExtensionBatchLoaderRegistry {
  private readonly loaders = new Map<string, ExtensionBatchLoader>();

  register(key: string, loader: ExtensionBatchLoader): void {
    if (this.loaders.has(key)) {
      throw new Error(`ExtensionBatchLoaderRegistry: "${key}" already has a registered batch loader`);
    }
    this.loaders.set(key, loader);
  }

  resolve(key: string): ExtensionBatchLoader {
    const loader = this.loaders.get(key);
    if (!loader) {
      throw new Error(`ExtensionBatchLoaderRegistry: no batch loader registered for "${key}"`);
    }
    return loader;
  }

  has(key: string): boolean {
    return this.loaders.has(key);
  }

  // See ComponentRegistry.unregister's own comment — same purpose here.
  unregister(key: string): void {
    this.loaders.delete(key);
  }
}

export const extensionBatchLoaderRegistry = new ExtensionBatchLoaderRegistry();
