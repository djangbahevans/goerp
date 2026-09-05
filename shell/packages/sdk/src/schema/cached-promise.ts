// Memoizes fetch's result for the process lifetime, clearing the cache
// on rejection so a failed attempt doesn't stick — shared by
// SchemaRegistry and ResourceRegistry, which would otherwise hand-roll
// the identical shape twice.
export function cacheUntilRejected<T>(fetch: () => Promise<T>): () => Promise<T> {
  let promise: Promise<T> | null = null;
  return () => {
    promise ??= fetch().catch((err: unknown) => {
      promise = null;
      throw err;
    });
    return promise;
  };
}
