import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { APIClient } from "../http/index.js";
import { apiClient } from "../http/index.js";
import type { ResourceMetadataRegistry } from "../schema/index.js";
import { resourceListPath, resourceMetadataRegistry } from "../schema/index.js";
import { ComboboxClearButton } from "./combobox-clear-button.js";
import { EmptyState } from "./empty-state.js";
import { EscapeLayer } from "./escape-layer.js";
import {
  optionElementId,
  useFloatingPanelPosition,
  useOutsideClickClose,
  useScrollHighlightedOptionIntoView,
} from "./floating-panel.js";
import type { RelationValue } from "./relation-field.js";
import { Skeleton } from "./skeleton.js";
import { TextInput } from "./text-input.js";

export type { RelationValue } from "./relation-field.js";

const PAGE_SIZE = 100;
// relation-picker.md: the search query is debounced by 300ms.
const DEBOUNCE_MS = 300;

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
  // dropdown instead of letting it float above the page.
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLSpanElement | null>(null);

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
      default:
        break;
    }
  }

  const position = useFloatingPanelPosition(isOpen, containerRef, panelRef, true);
  useOutsideClickClose(isOpen, [containerRef, panelRef], dismiss);
  useScrollHighlightedOptionIntoView(isOpen, listboxId, activeIndex, entries.length);

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
      <TextInput
        id={id}
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen && entries.length > 0 ? optionElementId(listboxId, activeIndex) : undefined}
        value={triggerValue}
        title={!isOpen && singleValue ? singleValue.display : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={handleFocus}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        // A single-select value has no pill row, so the clear button is the
        // only way to unset it back to null.
        end={
          !multiple && singleValue && !isOpen ? (
            <ComboboxClearButton label={singleValue.display} disabled={disabled} onClear={() => onChange(null)} />
          ) : undefined
        }
      />
      {isOpen &&
        createPortal(
          <EscapeLayer onEscape={dismiss}>
            <span
              ref={panelRef}
              id={listboxId}
              role="listbox"
              style={
                position
                  ? { position: "fixed", top: position.top, left: position.left, width: position.width }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className="z-(--z-dropdown) max-h-80 min-w-60 overflow-y-auto rounded-structural border border-border bg-surface p-2 shadow-md"
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
                    id={optionElementId(listboxId, index)}
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
                    {entry.kind === "option"
                      ? String(entry.row[labelField] ?? entry.row.id)
                      : `Create "${query.trim()}"`}
                  </div>
                ))
              )}
            </span>
          </EscapeLayer>,
          document.body,
        )}
    </div>
  );
}
