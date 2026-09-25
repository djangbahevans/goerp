import type { PagedResponse } from "./types.js";

// The engine's list envelope (record-activity.md §6, view-system.md §8):
// snake_case meta, and an empty-string cursor on the last page of an ORM list.
export interface PagedResponseWire<W> {
  data: W[];
  meta: { cursor?: string | null; has_more: boolean; total?: number | null };
}

export function toPagedResponse<W, T = W>(wire: PagedResponseWire<W>, mapItem?: (item: W) => T): PagedResponse<T> {
  const data = mapItem ? wire.data.map(mapItem) : (wire.data as unknown as T[]);
  return {
    data,
    meta: {
      cursor: wire.meta.cursor || null,
      hasMore: wire.meta.has_more,
      ...(wire.meta.total != null ? { total: wire.meta.total } : {}),
    },
  };
}
