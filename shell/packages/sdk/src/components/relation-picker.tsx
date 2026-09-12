import type { CSSProperties, KeyboardEvent, ReactNode } from "react";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { APIClient } from "../http/index.js";
import { apiClient } from "../http/index.js";
import type { ResourceMetadataRegistry } from "../schema/index.js";
import { resourceListPath, resourceMetadataRegistry } from "../schema/index.js";
import { EmptyState } from "./empty-state.js";
import { fieldInputClassName } from "./field-input-styles.js";
import type { RelationValue } from "./relation-field.js";
import { Skeleton } from "./skeleton.js";

export type { RelationValue } from "./relation-field.js";

const PAGE_SIZE = 100;
// relation-picker.md's own Open Question #1: no debounce timing is
// specified anywhere — this is a standard short debounce, not a
// documented value.
const DEBOUNCE_MS = 300;

// A single-select value has no pill/remove-button row the way multi-select
// does — the native <select> this replaces always had a blank "—" option,
// so this overlays a clear control on the trigger itself instead, the only
// way to unset a single-select value back to null.
const CLEAR_BUTTON_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "var(--space-2)",
  top: "50%",
  transform: "translateY(-50%)",
};

interface Row {
  id: string;
  [key: string]: unknown;
}

type PickerClient = Pick<APIClient, "get">;
type PickerRegistry = Pick<ResourceMetadataRegistry, "resolve">;

export interface RelationPickerProps {
  id?: string | undefined;
  resource: string;
  // Overrides the registry's resolved labelField for this picker
  // specifically (matches ListColumn.resource_label_field's override
  // semantics) — omit to use the resource's registry default.
  labelField?: string | undefined;
  resourceFilter?: Record<string, unknown> | undefined;
  value: RelationValue | RelationValue[] | null;
  onChange: (value: RelationValue | RelationValue[] | null) => void;
  multiple?: boolean | undefined;
  creatable?: boolean | undefined;
  onCreate?: ((name: string) => RelationValue | Promise<RelationValue>) | undefined;
  disabled?: boolean | undefined;
  placeholder?: string | undefined;
  client?: PickerClient | undefined;
  registry?: PickerRegistry | undefined;
}

// view-system.md §4: a scalar value is `filter[key]=v`; a non-empty array
// comma-joins under `[in]` instead of repeating the key.
function flattenResourceFilter(filter: Record<string, unknown> | undefined): Record<string, unknown> {
  const flat: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(filter ?? {})) {
    if (Array.isArray(value)) {
      if (value.length > 0) flat[`filter[${key}][in]`] = value.join(",");
    } else {
      flat[`filter[${key}]`] = value;
    }
  }
  return flat;
}

function toRelationValue(row: Row, labelField: string): RelationValue {
  return { id: row.id, display: String(row[labelField] ?? row.id) };
}

interface QueryResult {
  status: "loading" | "error" | "success";
  rows: Row[];
  labelField: string;
}

// No shared debounce hook exists in this codebase — use-record.ts's
// autosave delay uses the same raw setTimeout/useEffect idiom inline.
function useDebouncedValue(value: string, delayMs: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timeout = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timeout);
  }, [value, delayMs]);
  return debounced;
}

// A plain fetch-on-demand query, not react-query: packages/sdk has no
// QueryClientProvider dependency of its own (every other sdk component
// with async data — TagsField's options — takes them as a prop instead).
function useSearchQuery(
  resource: string,
  labelFieldOverride: string | undefined,
  query: string,
  resourceFilter: Record<string, unknown> | undefined,
  enabled: boolean,
  client: PickerClient,
  registry: PickerRegistry,
): QueryResult {
  const [result, setResult] = useState<QueryResult>({
    status: "loading",
    rows: [],
    labelField: labelFieldOverride ?? "",
  });
  const requestId = useRef(0);
  // A caller passing an inline object literal (`resourceFilter={{...}}`)
  // gets a new reference every render — memoizing on its serialized value
  // gives the effect below a reference that's actually stable across
  // renders with the same content, instead of refetching on every
  // unrelated re-render of the caller.
  // biome-ignore lint/correctness/useExhaustiveDependencies: intentionally keyed on the serialized value, not the resourceFilter reference itself — that's the whole point.
  const stableResourceFilter = useMemo(() => resourceFilter, [JSON.stringify(resourceFilter ?? null)]);

  useEffect(() => {
    if (!enabled) return;
    const thisRequest = ++requestId.current;
    setResult((prev) => ({ status: "loading", rows: prev.rows, labelField: prev.labelField }));

    (async () => {
      try {
        const entry = await registry.resolve(resource);
        const path = entry && resourceListPath(entry);
        if (!entry || !path) throw new Error(`RelationPicker: unregistered resource "${resource}"`);
        const labelField = labelFieldOverride ?? entry.labelField;
        const response = await client.get<{ data: Row[] }>(path, {
          params: {
            [entry.searchParam]: query === "" ? undefined : query,
            ...flattenResourceFilter(stableResourceFilter),
            limit: PAGE_SIZE,
          },
        });
        if (requestId.current === thisRequest) setResult({ status: "success", rows: response.data, labelField });
      } catch {
        if (requestId.current === thisRequest) setResult((prev) => ({ ...prev, status: "error", rows: [] }));
      }
    })();
  }, [enabled, resource, labelFieldOverride, query, stableResourceFilter, client, registry]);

  return result;
}

type Entry = { kind: "option"; row: Row } | { kind: "create" };

export function RelationPicker({
  id,
  resource,
  labelField: labelFieldOverride,
  resourceFilter,
  value,
  onChange,
  multiple = false,
  creatable = false,
  onCreate,
  disabled = false,
  placeholder,
  client = apiClient,
  registry = resourceMetadataRegistry,
}: RelationPickerProps): ReactNode {
  const [query, setQuery] = useState("");
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(0);
  const listboxId = useId();
  const debouncedQuery = useDebouncedValue(query, DEBOUNCE_MS);

  // Portaled to document.body, position: fixed — same reasoning as
  // ActionMenu's own panel: an ancestor with overflow: hidden (SectionCard's
  // own collapse-transition wrapper, e.g.) would otherwise clip the
  // dropdown instead of letting it float above the page. Null until
  // measured, so it renders hidden for one frame rather than flashing at
  // (0, 0).
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLSpanElement | null>(null);
  const [position, setPosition] = useState<{ top: number; left: number; width: number } | null>(null);

  const selected: RelationValue[] = multiple ? (Array.isArray(value) ? value : []) : [];
  const singleValue: RelationValue | null = multiple ? null : ((value as RelationValue | null) ?? null);
  const selectedIds = new Set(selected.map((v) => v.id));

  const { status, rows, labelField } = useSearchQuery(
    resource,
    labelFieldOverride,
    debouncedQuery,
    resourceFilter,
    isOpen,
    client,
    registry,
  );

  const matches = rows.filter((row) => !selectedIds.has(row.id));
  const normalizedQuery = debouncedQuery.trim().toLowerCase();
  const exactMatch = rows.some((row) => String(row[labelField] ?? "").toLowerCase() === normalizedQuery);
  const showCreate = creatable && normalizedQuery !== "" && !exactMatch && status === "success";
  const entries: Entry[] = [
    ...matches.map((row): Entry => ({ kind: "option", row })),
    ...(showCreate ? [{ kind: "create" } as const] : []),
  ];
  const activeIndex = Math.max(0, Math.min(highlightedIndex, entries.length - 1));

  function open(): void {
    if (disabled) return;
    setIsOpen(true);
    setHighlightedIndex(0);
  }

  function close(): void {
    setIsOpen(false);
    setQuery("");
  }

  // Escape and an outside click both dismiss without committing anything —
  // unlike close() (used only after a single-select commits), a multi-select
  // query stays as typed rather than being wiped by an incidental dismissal.
  function dismiss(): void {
    setIsOpen(false);
    if (!multiple) setQuery("");
  }

  function selectRow(row: Row): void {
    const relationValue = toRelationValue(row, labelField);
    if (multiple) {
      onChange([...selected, relationValue]);
      setQuery("");
      setHighlightedIndex(0);
    } else {
      onChange(relationValue);
      close();
    }
  }

  function removeSelected(id: string): void {
    onChange(selected.filter((v) => v.id !== id));
  }

  async function createFromQuery(): Promise<void> {
    if (!onCreate || normalizedQuery === "") return;
    const created = await onCreate(query.trim());
    if (multiple) {
      onChange([...selected, created]);
      setQuery("");
      setHighlightedIndex(0);
    } else {
      onChange(created);
      close();
    }
  }

  function handleInputChange(next: string): void {
    setQuery(next);
    if (!isOpen) open();
    else setHighlightedIndex(0);
  }

  function handleFocus(): void {
    open();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.nativeEvent.isComposing || disabled) return;

    if (multiple && event.key === "Backspace" && query === "" && selected.length > 0) {
      event.preventDefault();
      const last = selected[selected.length - 1];
      if (last) removeSelected(last.id);
      return;
    }

    if (!isOpen) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        open();
      }
      return;
    }

    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (entries.length > 0) setHighlightedIndex((activeIndex + 1) % entries.length);
        break;
      case "ArrowUp":
        event.preventDefault();
        if (entries.length > 0) setHighlightedIndex((activeIndex - 1 + entries.length) % entries.length);
        break;
      case "Enter": {
        event.preventDefault();
        const entry = entries[activeIndex];
        if (!entry) return;
        if (entry.kind === "option") selectRow(entry.row);
        else void createFromQuery();
        break;
      }
      case "Escape":
        event.preventDefault();
        dismiss();
        break;
      default:
        break;
    }
  }

  // Runs before paint, positioned once on open — not re-tracked on
  // scroll/resize, since the panel closes on Escape/selection/outside-click
  // well before either would matter (same simplification ActionMenu's own
  // panel makes).
  useLayoutEffect(() => {
    if (!isOpen) {
      setPosition(null);
      return;
    }
    const containerEl = containerRef.current;
    if (!containerEl) return;
    const containerRect = containerEl.getBoundingClientRect();
    const panelHeight = panelRef.current?.getBoundingClientRect().height ?? 0;
    const fitsBelow = containerRect.bottom + 4 + panelHeight <= window.innerHeight - 8;
    const top = fitsBelow ? containerRect.bottom + 4 : Math.max(8, containerRect.top - 4 - panelHeight);
    setPosition({ top, left: containerRect.left, width: containerRect.width });
  }, [isOpen]);

  // Closes on a click outside both the input/pills area and the portaled
  // panel — mousedown, not click, so it commits before any outside
  // element's own click handler fires (same reasoning ActionMenu's own
  // dismissal uses).
  // biome-ignore lint/correctness/useExhaustiveDependencies: dismiss is a plain function recreated every render, not a reactive dependency — only isOpen should re-arm this listener.
  useEffect(() => {
    if (!isOpen) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (containerRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      dismiss();
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [isOpen]);

  const triggerValue = isOpen ? query : (singleValue?.display ?? query);

  return (
    <div ref={containerRef} className="relative flex flex-col gap-1">
      {selected.length > 0 && (
        <span className="flex flex-wrap gap-1">
          {selected.map((item) => (
            <span
              key={item.id}
              className="inline-flex max-w-60 items-center gap-1 rounded-control bg-bg-subtle px-2 py-1 text-sm text-text"
            >
              <span className="truncate" title={item.display}>
                {item.display}
              </span>
              <button
                type="button"
                disabled={disabled}
                onClick={() => removeSelected(item.id)}
                aria-label={`Remove ${item.display}`}
                className="rounded-control p-1 transition-colors duration-(--duration-fast) ease-out hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
              >
                ×
              </button>
            </span>
          ))}
        </span>
      )}
      <input
        id={id}
        type="text"
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen && entries.length > 0 ? `${listboxId}-option-${activeIndex}` : undefined}
        value={triggerValue}
        title={!isOpen && singleValue ? singleValue.display : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={handleFocus}
        onChange={(event) => handleInputChange(event.target.value)}
        onKeyDown={handleKeyDown}
        style={!multiple && singleValue ? { paddingInlineEnd: "var(--space-8)" } : undefined}
        className={`truncate ${fieldInputClassName(false)}`}
      />
      {!multiple && singleValue && !isOpen && (
        <button
          type="button"
          disabled={disabled}
          onClick={() => onChange(null)}
          aria-label={`Clear ${singleValue.display}`}
          style={CLEAR_BUTTON_STYLE}
          className="rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
        >
          ×
        </button>
      )}
      {isOpen &&
        createPortal(
          <span
            ref={panelRef}
            id={listboxId}
            role="listbox"
            style={
              position
                ? { position: "fixed", top: position.top, left: position.left, width: position.width }
                : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
            }
            className="z-(--z-dropdown) min-w-60 rounded-structural border border-border bg-surface p-2 shadow-md"
          >
            {status === "error" ? (
              <EmptyState
                title="Module not installed"
                description={`The module providing "${resource}" isn't installed, so this field can't search it.`}
              />
            ) : status === "loading" ? (
              <Skeleton lines={3} />
            ) : entries.length === 0 ? (
              <span aria-live="polite" className="block px-2 py-1 text-sm text-text-secondary">
                No results for &quot;{debouncedQuery}&quot;
              </span>
            ) : (
              entries.map((entry, index) => (
                // biome-ignore lint/a11y/useFocusableInteractive: ARIA APG combobox-with-listbox — options are never independently focusable, only virtually "focused" via aria-activedescendant.
                // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection is handled by the input's own onKeyDown.
                <div
                  key={entry.kind === "option" ? entry.row.id : "create"}
                  id={`${listboxId}-option-${index}`}
                  role="option"
                  aria-selected={index === activeIndex}
                  aria-disabled={disabled}
                  onMouseEnter={disabled ? undefined : () => setHighlightedIndex(index)}
                  onClick={
                    disabled ? undefined : () => (entry.kind === "option" ? selectRow(entry.row) : createFromQuery())
                  }
                  title={entry.kind === "option" ? String(entry.row[labelField] ?? entry.row.id) : undefined}
                  className={`truncate rounded-control px-2 py-1 text-left text-sm text-text ${
                    disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer"
                  } ${index === activeIndex ? "bg-surface-hover" : ""}`}
                >
                  {entry.kind === "option" ? String(entry.row[labelField] ?? entry.row.id) : `Create "${query.trim()}"`}
                </div>
              ))
            )}
          </span>,
          document.body,
        )}
    </div>
  );
}
