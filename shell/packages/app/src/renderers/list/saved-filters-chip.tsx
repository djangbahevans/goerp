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
  const [saving, setSaving] = useState(false);
  const errorId = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const renameButtonRef = useRef<HTMLButtonElement | null>(null);
  // Pending and completed edits ignore additional Enter and blur submissions.
  const settledRef = useRef(false);
  const refocusRef = useRef(false);
  const editVersionRef = useRef(0);

  useEffect(() => {
    if (!editing && refocusRef.current) {
      refocusRef.current = false;
      renameButtonRef.current?.focus();
    }
  }, [editing]);

  function startEditing(): void {
    if (saving) return;
    editVersionRef.current += 1;
    settledRef.current = false;
    setDraft(filter.label);
    setError(null);
    setEditing(true);
  }

  function finish(refocus: boolean): void {
    editVersionRef.current += 1;
    settledRef.current = true;
    refocusRef.current = refocus;
    setEditing(false);
  }

  async function commit(refocus: boolean): Promise<void> {
    if (settledRef.current) return;
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
    setSaving(true);
    const version = editVersionRef.current;
    try {
      await onRename(filter, label);
      if (version === editVersionRef.current) {
        finish(refocus && document.activeElement === inputRef.current);
      }
    } catch (err) {
      if (version === editVersionRef.current) {
        settledRef.current = false;
        setError(err instanceof Error ? err.message : "Couldn't rename the filter.");
      }
    } finally {
      setSaving(false);
    }
  }

  if (editing) {
    return (
      // Its own layer, so Escape cancels the rename instead of closing the panel.
      <EscapeLayer onEscape={() => finish(true)}>
        <div className="px-2 py-1">
          <TextInput
            ref={inputRef}
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
            readOnly={saving}
            aria-busy={saving || undefined}
            onFocus={(event) => event.currentTarget.select()}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.nativeEvent.isComposing) {
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
      <span className="flex flex-none items-center gap-4 px-2 py-2 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 [&>button]:relative [&>button]:before:absolute [&>button]:before:-inset-2 [&>button]:before:content-['']">
        <IconButton
          ref={renameButtonRef}
          icon="pencil"
          label={`Rename '${filter.label}'`}
          size="sm"
          aria-disabled={saving || undefined}
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
  const [position, setPosition] = useState<{ top: number; left: number; maxHeight: number } | null>(null);
  const layerClassName = useFloatingPanelLayer(triggerRef);

  // biome-ignore lint/correctness/useExhaustiveDependencies: isLoading/filters.length are deliberate extra re-measure triggers, not read inside the effect.
  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const triggerEl = triggerRef.current;
    const panelEl = panelRef.current;
    if (!triggerEl || !panelEl) return;
    function measure(): void {
      if (!triggerEl || !panelEl) return;
      const triggerRect = triggerEl.getBoundingClientRect();
      const panelRect = panelEl.getBoundingClientRect();
      // Chrome's main region ends above the fixed mobile navigation.
      const viewportBottom = Math.min(
        window.innerHeight,
        triggerEl.closest("main")?.getBoundingClientRect().bottom ?? window.innerHeight,
      );
      const maxHeight = Math.max(0, viewportBottom - 16);
      const height = Math.min(panelRect.height, maxHeight);
      const maxLeft = window.innerWidth - panelRect.width - 8;
      const left = Math.max(8, Math.min(triggerRect.left, maxLeft));
      const fitsBelow = triggerRect.bottom + 4 + height <= viewportBottom - 8;
      const desiredTop = fitsBelow ? triggerRect.bottom + 4 : triggerRect.top - 4 - height;
      const top = Math.max(8, Math.min(desiredTop, viewportBottom - 8 - height));
      setPosition((prev) =>
        prev?.top === top && prev.left === left && prev.maxHeight === maxHeight ? prev : { top, left, maxHeight },
      );
    }
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(panelEl);
    observer?.observe(triggerEl);
    const mainEl = triggerEl.closest("main");
    if (mainEl) observer?.observe(mainEl);
    window.addEventListener("resize", measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [open, isLoading, filters.length]);

  useEffect(() => {
    if (open) headingRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open || saveDialogOpen) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      setOpen(false);
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [open, saveDialogOpen]);

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
                  ? { position: "fixed", top: position.top, left: position.left, maxHeight: position.maxHeight }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className={`${layerClassName} max-h-[calc(100dvh-16px)] min-w-40 max-w-70 overflow-y-auto rounded-structural border border-border bg-surface py-1 shadow-md`}
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
