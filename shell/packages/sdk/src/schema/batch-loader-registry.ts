// manifest-spec.md §8b Strategy 2 — module-provided, id-to-label resolution
// for one relation column, called once per page with that column's ids.
export type BatchLoader = (ids: string[]) => Promise<Map<string, string>>;

// Stand-in for defineModule().batchLoaders until real module registration
// exists (same provisional pattern as ComponentRegistry). Keyed by
// `${viewName}.${columnField}`, since a view can have several relation
// columns each needing a different loader.
export class BatchLoaderRegistry {
  private readonly loaders = new Map<string, BatchLoader>();

  register(key: string, loader: BatchLoader): void {
    if (this.loaders.has(key)) {
      throw new Error(`BatchLoaderRegistry: "${key}" already has a registered batch loader`);
    }
    this.loaders.set(key, loader);
  }

  resolve(key: string): BatchLoader {
    const loader = this.loaders.get(key);
    if (!loader) {
      throw new Error(`BatchLoaderRegistry: no batch loader registered for "${key}"`);
    }
    return loader;
  }

  has(key: string): boolean {
    return this.loaders.has(key);
  }
}
