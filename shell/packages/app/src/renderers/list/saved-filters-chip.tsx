import { AlertDialog, Icon, Skeleton } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { SavedFilter } from "@goerp/sdk/react";
import { useSavedFilters } from "@goerp/sdk/react";
import { defaultParseSearch } from "@tanstack/react-router";
import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { FilterValue, ListStateHandle } from "./use-list-state.js";
import { parseListSearch } from "./use-list-state.js";

export interface SavedFiltersChipProps {
  viewName: string;
  listState: ListStateHandle;
}

// Mirrors button-styles.ts's "secondary"/"sm" pieces, with
// --radius-full swapped in for --radius-control.
const TRIGGER_CLASSES = [
  "inline-flex h-7 items-center gap-2 rounded-full border border-border bg-surface px-2 font-medium text-sm text-text",
  "hover:bg-surface-hover hover:border-border-strong active:bg-surface-active",
  "transition-colors ease-out duration-(--duration-fast) motion-reduce:transition-none",
  "focus-visible:outline-none focus-visible:shadow-focus",
].join(" ");

const ROW_BUTTON_CLASSES =
  "flex min-w-0 flex-1 items-center gap-2 truncate rounded-control px-3 py-2 text-left text-sm text-text hover:bg-surface-hover focus-visible:outline-none focus-visible:shadow-focus";

// --space-12 (48px) hit area for a 14px icon — WCAG 2.5.5, rounded up since
// there's no 44px step in this scale.
const ICON_BUTTON_CLASSES =
  "flex h-(--space-12) w-(--space-12) flex-none items-center justify-center rounded-control text-text-secondary hover:bg-surface-hover focus-visible:outline-none focus-visible:shadow-focus";

function SavedFilterRow({
  filter,
  onApply,
  onSetDefault,
  onRemove,
}: {
  filter: SavedFilter;
  onApply: (filter: SavedFilter) => void;
  onSetDefault: (filter: SavedFilter) => void;
  onRemove: (filter: SavedFilter) => void;
}): ReactNode {
  return (
    <div className="group flex items-center gap-1 px-1">
      <button type="button" title={filter.label} className={ROW_BUTTON_CLASSES} onClick={() => onApply(filter)}>
        {filter.isDefault && (
          <Icon name="star" size={14} fill="currentColor" className="flex-none text-primary" aria-hidden="true" />
        )}
        <span className="truncate">{filter.label}</span>
      </button>
      <span className="flex flex-none items-center gap-2 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100">
        {!filter.isDefault && (
          <button
            type="button"
            aria-label={`Set '${filter.label}' as default`}
            className={ICON_BUTTON_CLASSES}
            onClick={() => onSetDefault(filter)}
          >
            <Icon name="star" size={14} aria-hidden="true" />
          </button>
        )}
        <button
          type="button"
          aria-label={`Delete '${filter.label}'`}
          className={`${ICON_BUTTON_CLASSES} text-danger`}
          onClick={() => onRemove(filter)}
        >
          <Icon name="trash-2" size={14} aria-hidden="true" />
        </button>
      </span>
    </div>
  );
}

export function SavedFiltersChip({ viewName, listState }: SavedFiltersChipProps): ReactNode {
  const { filters, isLoading, save, remove, setDefault } = useSavedFilters(viewName);
  const [open, setOpen] = useState(false);
  const [saveDialogOpen, setSaveDialogOpen] = useState(false);
  const headingId = useId();
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);

  // Re-measured on isLoading/filters.length too, not just open — the panel's
  // height changes once the fetch resolves or a row is deleted.
  // biome-ignore lint/correctness/useExhaustiveDependencies: isLoading/filters.length are deliberate extra re-measure triggers, not read inside the effect.
  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const triggerEl = triggerRef.current;
    const panelEl = panelRef.current;
    if (!triggerEl || !panelEl) return;
    const triggerRect = triggerEl.getBoundingClientRect();
    const panelRect = panelEl.getBoundingClientRect();
    const maxLeft = window.innerWidth - panelRect.width - 8;
    const left = Math.max(8, Math.min(triggerRect.left, maxLeft));
    const fitsBelow = triggerRect.bottom + 4 + panelRect.height <= window.innerHeight - 8;
    const top = fitsBelow ? triggerRect.bottom + 4 : Math.max(8, triggerRect.top - 4 - panelRect.height);
    setPosition({ top, left });
  }, [open, isLoading, filters.length]);

  useEffect(() => {
    if (open) headingRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      setOpen(false);
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [open]);

  function closeAndRefocus(): void {
    setOpen(false);
    triggerRef.current?.focus();
  }

  function handlePanelKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeAndRefocus();
    }
  }

  // Full replace, not a merge: any currently-set filter absent from the
  // target is explicitly cleared.
  function handleApply(filter: SavedFilter): void {
    const parsed = parseListSearch(defaultParseSearch(filter.queryString));
    const updates: Record<string, FilterValue | undefined> = { ...parsed.filter };
    for (const field of Object.keys(listState.filter)) {
      if (!(field in updates)) updates[field] = undefined;
    }
    listState.setFilters(updates);
    listState.setSort(parsed.sort);
    listState.setGroupBy(parsed.groupBy);
    closeAndRefocus();
  }

  // Errors already toast via use-saved-filters.ts's reportError.
  function handleSetDefault(filter: SavedFilter): void {
    setDefault(filter.id).catch(() => {});
  }

  function handleRemove(filter: SavedFilter): void {
    remove(filter.id).catch(() => {});
  }

  async function handleConfirmSave(name?: string): Promise<void> {
    if (!name) return;
    try {
      await save(name);
      toast.success("Filter saved");
      setSaveDialogOpen(false);
    } catch {
      // Already toasted; keep the dialog open so the typed name isn't lost.
    }
  }

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        className={TRIGGER_CLASSES}
        onClick={() => setOpen((prev) => !prev)}
      >
        <Icon name="bookmark" size={14} aria-hidden="true" />
        Saved filters
      </button>
      {open &&
        createPortal(
          <div
            ref={panelRef}
            role="dialog"
            aria-labelledby={headingId}
            onKeyDown={handlePanelKeyDown}
            style={
              position
                ? { position: "fixed", top: position.top, left: position.left }
                : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
            }
            className="z-(--z-dropdown) min-w-40 max-w-70 rounded-structural border border-border bg-surface py-1 shadow-md"
          >
            <h2
              id={headingId}
              ref={headingRef}
              tabIndex={-1}
              className="px-3 pt-2 pb-1 font-medium text-sm text-text focus:outline-none"
            >
              Saved filters
            </h2>
            {isLoading ? (
              <div className="px-3 py-2">
                <Skeleton lines={3} />
              </div>
            ) : filters.length === 0 ? (
              <p className="px-3 py-2 text-sm text-text-secondary">No saved filters yet.</p>
            ) : (
              filters.map((filter) => (
                <SavedFilterRow
                  key={filter.id}
                  filter={filter}
                  onApply={handleApply}
                  onSetDefault={handleSetDefault}
                  onRemove={handleRemove}
                />
              ))
            )}
            <button type="button" className={ROW_BUTTON_CLASSES} onClick={() => setSaveDialogOpen(true)}>
              <Icon name="plus" size={14} className="flex-none" aria-hidden="true" />
              Save current filter
            </button>
          </div>,
          document.body,
        )}
      <AlertDialog
        open={saveDialogOpen}
        title="Save current filter"
        description="Save the current filters, sort, and grouping as a new saved filter."
        confirmLabel="Save"
        input={{ label: "Name", type: "text", required: true }}
        onConfirm={(name) => void handleConfirmSave(name)}
        onCancel={() => setSaveDialogOpen(false)}
      />
    </>
  );
}
