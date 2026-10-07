import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { APIClient } from "../http/index.js";
import { apiClient } from "../http/index.js";
import type { ResourceMetadataRegistry } from "../schema/index.js";
import { resourceListPath, resourceMetadataRegistry } from "../schema/index.js";
import { Combobox } from "./combobox.js";
import { EmptyState } from "./empty-state.js";
import type { RelationValue } from "./relation-field.js";

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
  const debouncedQuery = useDebouncedValue(query, DEBOUNCE_MS);

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

  const labelOf = (row: Row) => String(row[labelField] ?? row.id);
  const matches = rows.filter((row) => !selectedIds.has(row.id));
  const normalizedQuery = debouncedQuery.trim().toLowerCase();
  const exactMatch = rows.some((row) => String(row[labelField] ?? "").toLowerCase() === normalizedQuery);
  const showCreate = creatable && normalizedQuery !== "" && !exactMatch && status === "success";
  const entries: Entry[] = [
    ...matches.map((row): Entry => ({ kind: "option", row })),
    ...(showCreate ? [{ kind: "create" } as const] : []),
  ];

  function commit(next: RelationValue): void {
    onChange(multiple ? [...selected, next] : next);
  }

  function removeSelected(id: string): void {
    onChange(selected.filter((v) => v.id !== id));
  }

  async function createFrom(name: string): Promise<void> {
    if (!onCreate || name === "") return;
    commit(await onCreate(name));
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.nativeEvent.isComposing || disabled) return;
    if (multiple && event.key === "Backspace" && query === "" && selected.length > 0) {
      event.preventDefault();
      const last = selected[selected.length - 1];
      if (last) removeSelected(last.id);
    }
  }

  const createLabel = `Create "${query.trim()}"`;

  return (
    <Combobox<Entry>
      id={id}
      query={query}
      onQueryChange={setQuery}
      options={entries}
      getOptionKey={(entry) => (entry.kind === "option" ? entry.row.id : "create")}
      getOptionLabel={(entry) => (entry.kind === "option" ? labelOf(entry.row) : createLabel)}
      onSelect={(entry) =>
        entry.kind === "option" ? commit(toRelationValue(entry.row, labelField)) : createFrom(query.trim())
      }
      countsAsResult={(entry) => entry.kind === "option"}
      status={status === "success" ? "ready" : status}
      errorContent={
        <EmptyState
          title="Module not installed"
          description={`The module providing "${resource}" isn't installed, so this field can't search it.`}
        />
      }
      emptyContent={
        <p className="px-2 py-1 text-sm text-text-secondary">No results for &quot;{debouncedQuery}&quot;</p>
      }
      selectedLabel={singleValue?.display}
      // A single-select value has no pill row, so the clear button is the
      // only way to unset it back to null.
      onClear={singleValue ? () => onChange(null) : undefined}
      above={
        selected.length > 0 ? (
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
                  className="inline-flex items-center justify-center rounded-control p-1 transition-colors duration-(--duration-fast) ease-out hover:opacity-75 focus-visible:shadow-focus focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 max-md:-my-3 max-md:min-h-11 max-md:min-w-11"
                >
                  ×
                </button>
              </span>
            ))}
          </span>
        ) : undefined
      }
      closeOnSelect={!multiple}
      keepQueryOnDismiss={multiple}
      onOpenChange={setIsOpen}
      onKeyDown={handleKeyDown}
      disabled={disabled}
      placeholder={placeholder}
    />
  );
}
