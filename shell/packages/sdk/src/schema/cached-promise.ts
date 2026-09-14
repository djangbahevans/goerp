export interface CachedPromise<T> {
  (): Promise<T>;
  // Forces the next call to re-run fetch instead of returning the
  // already-cached result — SchemaRegistry's own doc comment calls this
  // "the full view registry's concern," which is exactly what
  // ViewRegistryProvider's schema.updated/module.installed refresh (goerp#671)
  // uses this for: nothing else here clears the cache on demand, only on
  // rejection.
  invalidate(): void;
}

// Memoizes fetch's result for the process lifetime, clearing the cache
// on rejection so a failed attempt doesn't stick — shared by
// SchemaRegistry and ResourceRegistry, which would otherwise hand-roll
// the identical shape twice.
export function cacheUntilRejected<T>(fetch: () => Promise<T>): CachedPromise<T> {
  let promise: Promise<T> | null = null;
  const cached: CachedPromise<T> = () => {
    promise ??= fetch().catch((err: unknown) => {
      promise = null;
      throw err;
    });
    return promise;
  };
  cached.invalidate = () => {
    promise = null;
  };
  return cached;
}
