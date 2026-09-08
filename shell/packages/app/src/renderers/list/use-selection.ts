import { useCallback, useState } from "react";

// Row selection for bulk actions (manifest-spec.md's `selectable`) —
// always local component state, never URL-persisted like filter/sort:
// a selection is ephemeral UI state tied to what's currently on screen,
// not something worth bookmarking or sharing.
export interface SelectionHandle {
  selectedIds: Set<string>;
  isSelected: (id: string) => boolean;
  toggle: (id: string) => void;
  toggleAll: (ids: string[]) => void;
  clear: () => void;
}

export function useSelection(): SelectionHandle {
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const isSelected = useCallback((id: string) => selectedIds.has(id), [selectedIds]);

  const toggle = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  // Selects every given id when any is currently unselected, otherwise
  // clears them all — the standard "select all" checkbox tri-state
  // collapse (indeterminate -> all selected; all selected -> none).
  const toggleAll = useCallback((ids: string[]) => {
    setSelectedIds((prev) => {
      const allSelected = ids.length > 0 && ids.every((id) => prev.has(id));
      return allSelected ? new Set() : new Set(ids);
    });
  }, []);

  const clear = useCallback(() => setSelectedIds(new Set()), []);

  return { selectedIds, isSelected, toggle, toggleAll, clear };
}
