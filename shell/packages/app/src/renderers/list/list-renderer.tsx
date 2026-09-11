import type { FilterRange } from "@goerp/sdk";
import type { RelationBatchSpec } from "@goerp/sdk/react";
import { useInfiniteList, useRelationLabels } from "@goerp/sdk/react";
import { useEffect, useMemo, useRef } from "react";
import { BulkActions } from "./bulk-actions.js";
import { columnStyle, renderCell } from "./column-renderers.js";
import { ListActions } from "./list-actions.js";
import { isMultiValueFilter, ListFilters } from "./list-filters.js";
import type { ListColumn, ListFilter, ListViewDeclaration, Row } from "./list-view-types.js";
import type { FilterValue } from "./use-list-state.js";
import { useListState } from "./use-list-state.js";
import { useSelection } from "./use-selection.js";
import { useVisibleColumns } from "./use-visible-columns.js";

// shell-architecture.md §20's ListRenderer — mode switching, URL/local
// state, field security, and (goerp#575) column/filter/action rendering.
export interface ListRendererProps {
  view: ListViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
}

function defaultSortOf(view: ListViewDeclaration): string | undefined {
  if (!view.default_sort) return undefined;
  return view.default_sort_dir === "desc" ? `-${view.default_sort}` : view.default_sort;
}

// Accepts numeric bounds too — manifest-spec.md types `default`/
// `default_filters` as `any`, and a number_range filter's natural default
// is numeric, not pre-stringified.
function coerceRangeBounds(raw: unknown): FilterRange | undefined {
  if (typeof raw !== "object" || raw === null) return undefined;
  const { gte, lte } = raw as { gte?: unknown; lte?: unknown };
  const range: FilterRange = {};
  if (typeof gte === "string" || typeof gte === "number") range.gte = String(gte);
  if (typeof lte === "string" || typeof lte === "number") range.lte = String(lte);
  return Object.keys(range).length > 0 ? range : undefined;
}

// A `Filter.default`'s raw JSON value, wrapped to match how its type
// serializes (manifest-spec.md's `default` field carries no shape info of
// its own).
function coerceFilterDefault(filter: ListFilter, raw: unknown): FilterValue | undefined {
  if (filter.type === "text") return typeof raw === "string" ? { like: raw } : undefined;
  if (filter.type === "daterange" || filter.type === "number_range") return coerceRangeBounds(raw);
  if (isMultiValueFilter(filter)) return Array.isArray(raw) ? raw.map(String) : undefined;
  if (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean") return raw;
  return undefined;
}

// `default_filters` values have no per-field type context the way a
// Filter object's own `default` does — a range-shaped object is inferred
// from its own {gte,lte} shape rather than a declared filter type.
function coerceDefaultFiltersValue(raw: unknown): FilterValue | undefined {
  if (Array.isArray(raw)) return raw.map(String);
  if (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean") return raw;
  return coerceRangeBounds(raw);
}

// manifest-spec.md §9.1: default_filters wins over a Filter's own default
// on the same field, applied only when the view first loads with no
// filter[...] params already present.
export function computeDefaultFilters(view: ListViewDeclaration): Record<string, FilterValue> {
  const defaults: Record<string, FilterValue> = {};

  for (const [field, raw] of Object.entries(view.default_filters ?? {})) {
    const value = coerceDefaultFiltersValue(raw);
    if (value !== undefined) defaults[field] = value;
  }

  for (const filter of view.filters ?? []) {
    if (filter.default === undefined || filter.field in defaults) continue;
    const value = coerceFilterDefault(filter, filter.default);
    if (value !== undefined) defaults[filter.field] = value;
  }

  return defaults;
}

// Every relation column without `display_field` resolves via
// useRelationLabels (batch loader, else registry auto-fetch — manifest-spec.md §8b).
//
// One spec per (column, already-fetched page), all sharing the column's
// field as their `key` (useRelationLabels merges same-key results). Each
// page's own id set never changes once that page is loaded, so its query
// stays cached forever — fetchNextPage only ever issues a fresh, small
// query for the new page's ids, never re-fetching labels already resolved
// for earlier pages.
function relationBatchSpecs(viewName: string, columns: ListColumn[], pages: Row[][]): RelationBatchSpec[] {
  const relationColumns = columns.filter((c) => c.type === "relation" && !c.display_field && c.resource);
  return relationColumns.flatMap((c) =>
    pages.map((pageRows) => ({
      key: c.field,
      resource: c.resource as string,
      ...(c.resource_label_field ? { labelField: c.resource_label_field } : {}),
      view: viewName,
      ids: pageRows.map((row) => row[c.field]).filter((value): value is string => typeof value === "string"),
    })),
  );
}

export interface RowGroup {
  key: string;
  rows: Row[];
}

// view-system.md's Groups: bucket the already-fetched page client-side by
// the grouped field's value — display grouping, independent of whether
// the module also grouped server-side.
export function groupRows(rows: Row[], groupBy: string | undefined): RowGroup[] {
  if (!groupBy) return [{ key: "", rows }];
  const order: string[] = [];
  const byKey = new Map<string, Row[]>();
  for (const row of rows) {
    const key = String(row[groupBy] ?? "");
    if (!byKey.has(key)) {
      byKey.set(key, []);
      order.push(key);
    }
    byKey.get(key)?.push(row);
  }
  return order.map((key) => ({ key, rows: byKey.get(key) ?? [] }));
}

export function ListRenderer({ view, module, recordId, embedded, baseFilter }: ListRendererProps) {
  const listState = useListState(embedded, defaultSortOf(view));
  const columns = useVisibleColumns(view);
  const selection = useSelection();

  // Applied once, only when the view opens with no filter[...] params
  // already present — a later, user-driven clear-to-empty must not
  // re-trigger this.
  const defaultsApplied = useRef(false);
  const { setFilters } = listState;
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount, guarded by defaultsApplied — view/listState.filter/setFilters are deliberately read only at that first run, not tracked as change-triggers.
  useEffect(() => {
    if (defaultsApplied.current) return;
    defaultsApplied.current = true;
    if (Object.keys(listState.filter).length > 0) return;
    const defaults = computeDefaultFilters(view);
    if (Object.keys(defaults).length > 0) setFilters(defaults);
  }, []);

  const bulkActions = view.bulk_actions ?? [];
  // manifest-spec.md: `selectable` defaults true, but a checkbox column
  // with nothing to bulk-act on is just clutter.
  const showSelection = view.selectable !== false && bulkActions.length > 0;

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };
  const filterKey = JSON.stringify(filter);

  // A selection is scoped to the currently-visible rows — once the filter
  // changes the dataset out from under it, a stale id could still fire a
  // bulk action against a record the user can no longer see or intended
  // to include. Sort changes reorder the same rows, so they're excluded.
  const { clear: clearSelection } = selection;
  // biome-ignore lint/correctness/useExhaustiveDependencies: filterKey is a deliberate change-trigger, not read inside the effect.
  useEffect(() => {
    clearSelection();
  }, [filterKey, clearSelection]);

  const selectedIds = useMemo(() => [...selection.selectedIds], [selection.selectedIds]);

  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading, isError, error, refetch } =
    useInfiniteList<Row>(view.resource, {
      filter,
      ...(listState.sort !== undefined ? { sort: listState.sort } : {}),
      ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
    });

  const pages = data?.pages.map((page) => page.data) ?? [];
  const rows = pages.flat();
  const relationLabels = useRelationLabels(relationBatchSpecs(`${module}.${view.name}`, columns, pages));

  if (isLoading) {
    return (
      <div role="status" aria-label={`Loading ${view.label}`}>
        Loading…
      </div>
    );
  }

  if (isError) {
    return (
      <div role="alert">
        <p>Couldn't load {view.label}.</p>
        {error && <p>{error.message}</p>}
        <button type="button" onClick={() => refetch()}>
          Retry
        </button>
      </div>
    );
  }

  const groupByOptions = view.group_by_options ?? [];

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      <ListActions actions={view.actions ?? []} module={module} />
      {showSelection && <BulkActions actions={bulkActions} selectedIds={selectedIds} clearSelection={clearSelection} />}
      {groupByOptions.length > 0 && (
        <label>
          Group by
          <select
            value={listState.groupBy ?? ""}
            onChange={(event) => listState.setGroupBy(event.target.value || undefined)}
          >
            <option value="">None</option>
            {groupByOptions.map((field) => (
              <option key={field} value={field}>
                {field}
              </option>
            ))}
          </select>
        </label>
      )}
      {rows.length === 0 ? (
        <div role="status">No {view.label.toLowerCase()} found.</div>
      ) : (
        groupRows(rows, listState.groupBy).map((group) => {
          const selectableIds = group.rows.map((row) => row.id).filter((id): id is string => typeof id === "string");
          return (
            <table aria-label={view.label} key={group.key}>
              {listState.groupBy && (
                <caption>
                  {listState.groupBy} = {group.key}
                </caption>
              )}
              <thead>
                <tr>
                  {showSelection && (
                    <th scope="col">
                      <input
                        type="checkbox"
                        aria-label={
                          listState.groupBy ? `Select all in ${group.key}` : `Select all ${view.label.toLowerCase()}`
                        }
                        checked={selectableIds.length > 0 && selectableIds.every((id) => selection.selectedIds.has(id))}
                        onChange={() => selection.toggleAll(selectableIds)}
                      />
                    </th>
                  )}
                  {columns.map((column) => (
                    <th scope="col" key={column.field} style={columnStyle(column)}>
                      {column.label ?? column.field}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {group.rows.map((row, index) => (
                  <tr key={(row.id as string | undefined) ?? index}>
                    {showSelection && (
                      <td>
                        {typeof row.id === "string" && (
                          <input
                            type="checkbox"
                            aria-label="Select row"
                            checked={selection.selectedIds.has(row.id)}
                            onChange={() => selection.toggle(row.id as string)}
                          />
                        )}
                      </td>
                    )}
                    {columns.map((column) => {
                      const rawValue = row[column.field];
                      const relationLabel =
                        typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
                      return (
                        <td key={column.field} style={columnStyle(column)}>
                          {renderCell(column, row, relationLabel !== undefined ? { relationLabel } : {})}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          );
        })
      )}
      {hasNextPage && (
        <button type="button" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
          {isFetchingNextPage ? "Loading…" : "Load more"}
        </button>
      )}
    </>
  );
}
