import {
  AlertDialog,
  Button,
  EscapeLayer,
  Icon,
  IconButton,
  Skeleton,
  TextInput,
  useFloatingPanelLayer,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { SavedFilter } from "@goerp/sdk/react";
import { useSavedFilters } from "@goerp/sdk/react";
import { defaultParseSearch } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { FilterValue, ListStateHandle } from "./use-list-state.js";
import { parseListSearch } from "./use-list-state.js";

export interface SavedFiltersChipProps {
  viewName: string;
  listState: ListStateHandle;
}

const ROW_BUTTON_CLASSES =
  "flex min-w-0 flex-1 items-center gap-2 truncate rounded-control px-3 py-2 text-left text-sm text-text hover:bg-surface-hover focus-visible:outline-none focus-visible:shadow-focus";

function SavedFilterRow({
  filter,
  onApply,
  onRename,
  onSetDefault,
  onRemove,
}: {
  filter: SavedFilter;
  onApply: (filter: SavedFilter) => void;
  onRename: (filter: SavedFilter, label: string) => Promise<void>;
  onSetDefault: (filter: SavedFilter) => void;
  onRemove: (filter: SavedFilter) => void;
}): ReactNode {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const errorId = useId();
  const renameButtonRef = useRef<HTMLButtonElement | null>(null);
  // Set while a save is in flight and once the edit ends, so a blur right
  // after Enter (or on unmount) can't save or cancel a second time.
  const settledRef = useRef(false);
  const refocusRef = useRef(false);

  useEffect(() => {
    if (!editing && refocusRef.current) {
      refocusRef.current = false;
      renameButtonRef.current?.focus();
    }
  }, [editing]);

  function startEditing(): void {
    settledRef.current = false;
    setDraft(filter.label);
    setError(null);
    setEditing(true);
  }

  function finish(refocus: boolean): void {
    settledRef.current = true;
    refocusRef.current = refocus;
    setEditing(false);
  }

  async function commit(refocus: boolean): Promise<void> {
    const label = draft.trim();
    if (label === filter.label) {
      finish(refocus);
      return;
    }
    if (label === "") {
      setError("Enter a name.");
      return;
    }
    settledRef.current = true;
    try {
      await onRename(filter, label);
      finish(refocus);
    } catch (err) {
      settledRef.current = false;
      setError(err instanceof Error ? err.message : "Couldn't rename the filter.");
    }
  }

  if (editing) {
    return (
      // Its own layer, so Escape cancels the rename instead of closing the panel.
      <EscapeLayer onEscape={() => finish(true)}>
        <div className="px-2 py-1">
          <TextInput
            size="sm"
            value={draft}
            onChange={(value) => {
              setDraft(value);
              setError(null);
            }}
            aria-label={`Rename '${filter.label}'`}
            invalid={error !== null}
            aria-describedby={error !== null ? errorId : undefined}
            autoFocus
            onFocus={(event) => event.currentTarget.select()}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void commit(true);
              }
            }}
            onBlur={() => {
              if (settledRef.current) return;
              if (draft.trim() === filter.label) finish(false);
              else void commit(false);
            }}
          />
          {error !== null && (
            <p id={errorId} role="alert" className="mt-1 text-danger text-xs">
              {error}
            </p>
          )}
        </div>
      </EscapeLayer>
    );
  }

  return (
    <div className="group flex items-center gap-1 px-1">
      <button type="button" title={filter.label} className={ROW_BUTTON_CLASSES} onClick={() => onApply(filter)}>
        {filter.isDefault && (
          <Icon name="star" size={14} fill="currentColor" className="flex-none text-primary" aria-hidden="true" />
        )}
        <span className="truncate">{filter.label}</span>
      </button>
      <span className="flex flex-none items-center gap-2 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100">
        <IconButton
          ref={renameButtonRef}
          icon="pencil"
          label={`Rename '${filter.label}'`}
          size="sm"
          onClick={startEditing}
        />
        {!filter.isDefault && (
          <IconButton
            icon="star"
            label={`Set '${filter.label}' as default`}
            size="sm"
            onClick={() => onSetDefault(filter)}
          />
        )}
        <IconButton
          icon="trash-2"
          label={`Delete '${filter.label}'`}
          variant="danger"
          size="sm"
          onClick={() => onRemove(filter)}
        />
      </span>
    </div>
  );
}

export function SavedFiltersChip({ viewName, listState }: SavedFiltersChipProps): ReactNode {
  const { filters, isLoading, save, remove, setDefault, rename } = useSavedFilters(viewName);
  const [open, setOpen] = useState(false);
  const [saveDialogOpen, setSaveDialogOpen] = useState(false);
  const headingId = useId();
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const layerClassName = useFloatingPanelLayer(triggerRef);

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

  // Rejects on failure, for the row to show the error under its input.
  async function handleRename(filter: SavedFilter, label: string): Promise<void> {
    await rename(filter.id, label);
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
      <Button
        ref={triggerRef}
        variant="secondary"
        icon="bookmark"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((prev) => !prev)}
      >
        Saved filters
      </Button>
      {open &&
        createPortal(
          <EscapeLayer onEscape={closeAndRefocus}>
            <div
              ref={panelRef}
              role="dialog"
              aria-labelledby={headingId}
              style={
                position
                  ? { position: "fixed", top: position.top, left: position.left }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className={`${layerClassName} min-w-40 max-w-70 rounded-structural border border-border bg-surface py-1 shadow-md`}
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
                    onRename={handleRename}
                    onSetDefault={handleSetDefault}
                    onRemove={handleRemove}
                  />
                ))
              )}
              <button type="button" className={ROW_BUTTON_CLASSES} onClick={() => setSaveDialogOpen(true)}>
                <Icon name="plus" size={14} className="flex-none" aria-hidden="true" />
                Save current filter
              </button>
            </div>
          </EscapeLayer>,
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
