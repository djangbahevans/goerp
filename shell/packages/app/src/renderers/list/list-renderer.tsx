import type { FilterRange } from "@goerp/sdk";
import { ActionButton, ActionMenu, EmptyState, Icon, Select, Skeleton } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import type { RelationBatchSpec } from "@goerp/sdk/react";
import { useInfiniteList, useRelationLabels } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, ChevronRight, ChevronUp } from "lucide-react";
import type { CSSProperties, KeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import { Fragment, useEffect, useId, useMemo, useRef, useState } from "react";
import { BulkActions } from "./bulk-actions.js";
import { columnStyle, renderCell, shouldTruncate } from "./column-renderers.js";
import { ListActions } from "./list-actions.js";
import { isMultiValueFilter, ListFilters } from "./list-filters.js";
import type { ListColumn, ListFilter, ListViewDeclaration, Row } from "./list-view-types.js";
import type { FilterValue } from "./use-list-state.js";
import { useListState } from "./use-list-state.js";
import { useSelection } from "./use-selection.js";
import type { TreeRow } from "./use-tree-rows.js";
import { useTreeRows } from "./use-tree-rows.js";
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

// Wraps a flat view's rows in TreeRow's shape so the table body below has
// one rendering path for both cases.
function toFlatTreeRows(rows: Row[]): TreeRow[] {
  return rows.map((row) => ({
    row,
    depth: 0,
    isExpanded: false,
    isLoadingChildren: false,
    hasChildrenUnknown: false,
    hasChildren: false,
    hasError: false,
  }));
}

export type SortDirection = "asc" | "desc";

// listState.sort is a single "field" (asc) or "-field" (desc) string —
// only one column can be the active sort at a time.
export function sortDirectionOf(sort: string | undefined, field: string): SortDirection | undefined {
  if (sort === field) return "asc";
  if (sort === `-${field}`) return "desc";
  return undefined;
}

// asc -> desc -> unsorted, the conventional three-state cycle for a
// sortable column header.
export function nextSortValue(sort: string | undefined, field: string): string | undefined {
  const direction = sortDirectionOf(sort, field);
  if (direction === undefined) return field;
  if (direction === "asc") return `-${field}`;
  return undefined;
}

// manifest-spec.md's row_click/row_click_param: `row_click` resolves via
// viewPathRegistry to a RouteSchema path (shell-architecture.md's
// expanded-path convention, e.g. "/contacts/{id}") — a bare `{id}` token,
// not the `{record.field}` templating column.href/renderHref use for a
// different purpose (manifest-driven href/format strings). `row_click_param`
// only says which row field supplies that id value, default "id".
export function rowClickHref(resolvedPath: string | null, row: Row, param: string): string | undefined {
  if (!resolvedPath) return undefined;
  const idValue = row[param];
  if (typeof idValue !== "string" && typeof idValue !== "number") return undefined;
  return moduleLink(resolvedPath.replace("{id}", String(idValue)));
}

// column-renderers.tsx's renderCellContent already wraps these types (or
// any column with `href` set) in their own <a> — wrapping the primary
// column's cell in a second, row-click <a> on top would nest anchors,
// which browsers parse by implicitly closing the outer one where the
// inner starts, breaking both links. The primary column stays a plain
// cell in this case; row_click has no link to attach to for that row.
export function columnRendersOwnLink(column: ListColumn): boolean {
  return (
    column.href !== undefined ||
    column.type === "email" ||
    column.type === "phone" ||
    column.type === "url" ||
    column.type === "file"
  );
}

// text-overflow: ellipsis doesn't reliably clip on a display: table-cell
// box (columnStyle's own overflow/ellipsis/white-space trio, applied
// directly to a <th>/<td>) across browsers — it needs a block-level
// element to actually establish the overflow context. Applied to an
// inner wrapper around the cell's content instead of the cell itself.
// shouldTruncate (column-renderers.tsx) is the one place that decides
// which columns truncate at all — kept out of sync with columnStyle's own
// overflow-hidden and this would clip a "json" cell's <details> expando
// shut from one side while leaving it open from the other.
function truncationStyle(column: ListColumn): CSSProperties {
  if (!shouldTruncate(column)) return {};
  return { display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };
}

// list-renderer.md's own edge case: a truncated cell carries a native
// title with its full value. Measured lazily on hover (scrollWidth vs.
// clientWidth against the element's own live textContent) rather than
// once at mount: a mount-time measurement would go stale the moment
// content resolves asynchronously after the initial render (e.g. a
// relation column's id-to-label resolution, use-relation-labels.ts) since
// React only re-invokes a stable-identity ref callback on mount/unmount,
// not on every update — and reading layout for every cell on every mount
// forces a reflow per cell, which a rarely-triggered hover handler avoids
// entirely until a user actually points at one.
function handleTruncationHover(event: ReactMouseEvent<HTMLElement>) {
  const el = event.currentTarget;
  el.title = el.scrollWidth > el.clientWidth ? (el.textContent ?? "") : "";
}

// A column with no declared manifest width would otherwise get whatever
// width its own group's row content happens to produce — each group
// renders as its own independent <table> (list-renderer.md's own
// accessibility note: unambiguous group boundaries), so two groups with
// different content lengths could size the same column differently even
// though they're meant to line up. Each table's <colgroup> (below) pins
// every column to this shared width instead, so every group's table
// lands on identical column widths regardless of its own content, and
// still grows past its container into data-table.md's horizontal-scroll
// treatment once there are enough columns.
const DEFAULT_COLUMN_WIDTH = 160;
// A checkbox never needs a data column's width — a fixed, narrow width of
// its own (same reasoning as DEFAULT_COLUMN_WIDTH above, just sized for a
// single checkbox plus padding instead of arbitrary cell content).
const CHECKBOX_COLUMN_WIDTH = 44;

export function ListRenderer({ view, module, recordId, embedded, baseFilter }: ListRendererProps) {
  const listState = useListState(embedded, defaultSortOf(view));
  const { columns, hiddenColumns, revealedFields, toggleColumn } = useVisibleColumns(view);
  const selection = useSelection();
  const navigate = useNavigate();
  const groupBySelectId = useId();
  // data-table.md's horizontal-scroll treatment, extended here so every
  // group's <table> shares one scroll position instead of each scrolling
  // independently — plus a sticky, shadowed selection-checkbox column
  // (list-renderer.md's own extension) so selecting rows doesn't require
  // scrolling back to the start.
  const [scrolled, setScrolled] = useState(false);

  const rowClickView = view.row_click;
  const [rowClickPath, setRowClickPath] = useState<string | null>(null);
  useEffect(() => {
    if (!rowClickView) {
      setRowClickPath(null);
      return;
    }
    let cancelled = false;
    viewPathRegistry
      .resolve(rowClickView, module)
      .then((path) => {
        if (!cancelled) setRowClickPath(path);
      })
      .catch(() => {
        // row_click degrades to "no navigation" rather than crashing the
        // whole list over a schema fetch failure — same posture as an
        // unregistered relation resource elsewhere in this renderer.
        if (!cancelled) setRowClickPath(null);
      });
    return () => {
      cancelled = true;
    };
  }, [rowClickView, module]);
  const rowClickParam = view.row_click_param ?? "id";

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
  // view-system.md §4 "Hierarchical lists (tree_field)" — mutually
  // exclusive with group_by_options in practice.
  const isTree = view.tree_field !== undefined;

  // A row that also carries a selection checkbox or a tree expand/collapse
  // chevron can't claim its own tabIndex={0} click target too (either one
  // competes with the row-navigate action for the same Enter/Space press)
  // — the `primary: true` column's cell becomes the real link instead. See
  // docs/components/list-renderer.md's row-interaction States entry.
  const primaryColumnField = columns.find((column) => column.primary)?.field;
  const wholeRowClickable = Boolean(rowClickPath) && !showSelection && !isTree;
  // manifest-spec.md documents `primary` and `row_click` independently,
  // with no stated dependency between them — but with a selection checkbox
  // or tree chevron present, row_click has no cell to attach a link to
  // without a primary column, and silently doing nothing over a manifest
  // oversight like that is worth a dev-time signal rather than staying
  // invisible.
  useEffect(() => {
    if ((showSelection || isTree) && rowClickPath && primaryColumnField === undefined) {
      console.warn(
        `ListRenderer: view "${view.name}" declares row_click but has no column marked primary: true — with a selection checkbox or tree chevron present, there's no cell for row_click to navigate from.`,
      );
    }
  }, [showSelection, isTree, rowClickPath, primaryColumnField, view.name]);

  // Both fields are independently optional on ListViewDeclaration, but
  // tree_field always wins and group_by_options is silently ignored — a
  // manifest declaring both is worth a dev-time signal, the same posture
  // as the row_click warning above.
  useEffect(() => {
    if (isTree && (view.group_by_options?.length ?? 0) > 0) {
      console.warn(
        `ListRenderer: view "${view.name}" declares both tree_field and group_by_options — tree_field wins, group_by_options is ignored.`,
      );
    }
  }, [isTree, view.group_by_options, view.name]);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };
  const filterKey = JSON.stringify(filter);

  // goerp#791's isnull operator fetches root rows only — but not when a
  // baseFilter already constrains the tree field (an embedded subtree),
  // since a locked baseFilter always wins per the contract above.
  const rootFilter =
    view.tree_field !== undefined && !(view.tree_field in filter)
      ? { ...filter, [view.tree_field]: { isnull: true } }
      : filter;

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
      filter: rootFilter,
      ...(listState.sort !== undefined ? { sort: listState.sort } : {}),
      ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
    });

  const pages = data?.pages.map((page) => page.data) ?? [];
  // Memoized on `data` itself, not `pages` — otherwise useTreeRows' auto-
  // expand effect re-runs its tree walk on every render, not just when the
  // row set changes.
  const rootRows = useMemo(() => (data?.pages.map((page) => page.data) ?? []).flat(), [data]);
  const { treeRows, toggleExpand, retryChildren, fetchedPages } = useTreeRows(
    view.resource,
    view.tree_field ?? "",
    view.default_expanded_depth ?? 0,
    isTree ? rootRows : [],
    filter,
    listState.sort,
  );
  const visibleRows = isTree ? treeRows : toFlatTreeRows(rootRows);
  // Root pages (unflattened, so "Load more" doesn't invalidate an earlier
  // page's already-resolved labels) plus each expanded node's own children
  // fetch — see fetchedPages' own doc comment for why each needs to stay
  // its own stable-id-set page.
  const relationLabels = useRelationLabels(
    relationBatchSpecs(`${module}.${view.name}`, columns, isTree ? [...pages, ...fetchedPages] : pages),
  );

  if (isLoading) {
    return <Skeleton type="table" columns={columns.length} />;
  }

  if (isError) {
    return (
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load {view.label}.</p>
        {error && <p className="text-sm text-text-secondary">{error.message}</p>}
        <ActionButton
          variant="secondary"
          onClick={() => {
            void refetch();
          }}
        >
          Retry
        </ActionButton>
      </div>
    );
  }

  const groupByOptions = view.group_by_options ?? [];
  const groupBySelectOptions = [
    { value: "", label: "None" },
    ...groupByOptions.map((field) => ({ value: field, label: field })),
  ];

  const stickyCheckboxClassName = "sticky left-0 z-10";
  const tableWidth =
    (showSelection ? CHECKBOX_COLUMN_WIDTH : 0) +
    columns.reduce((sum, column) => sum + (column.width ?? DEFAULT_COLUMN_WIDTH), 0);

  // A tree_field view always renders as one hierarchy — group_by_options
  // and tree_field are mutually exclusive in practice (list-view-types.ts's
  // own comment), so tree mode skips groupRows entirely rather than trying
  // to combine the two bucketings of the same rows.
  const rowGroups = isTree
    ? [{ key: "", rows: treeRows }]
    : groupRows(rootRows, listState.groupBy).map((group) => ({ key: group.key, rows: toFlatTreeRows(group.rows) }));

  function navigateToRow(row: Row) {
    const href = rowClickHref(rowClickPath, row, rowClickParam);
    if (href) void navigate({ to: href });
  }

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      <div className="flex items-center justify-between gap-2">
        <ListActions actions={view.actions ?? []} module={module} />
        {hiddenColumns.length > 0 && (
          <ActionMenu
            label="Columns"
            items={hiddenColumns.map((column) => ({
              label: column.label ?? column.field,
              checked: revealedFields.has(column.field),
              onClick: () => toggleColumn(column.field),
            }))}
          />
        )}
      </div>
      {showSelection && <BulkActions actions={bulkActions} selectedIds={selectedIds} clearSelection={clearSelection} />}
      {!isTree && groupByOptions.length > 0 && (
        <label htmlFor={groupBySelectId} className="flex items-center gap-2 text-sm text-text-secondary">
          Group by
          <Select
            id={groupBySelectId}
            options={groupBySelectOptions}
            value={listState.groupBy ?? ""}
            onChange={(value) => {
              const next = Array.isArray(value) ? value[0] : value;
              listState.setGroupBy(next !== undefined && next !== "" ? next : undefined);
            }}
          />
        </label>
      )}
      {visibleRows.length === 0 ? (
        <EmptyState
          title={view.empty_state?.title ?? `No ${view.label.toLowerCase()} found.`}
          {...(view.empty_state?.description !== undefined ? { description: view.empty_state.description } : {})}
        />
      ) : (
        <div className="overflow-x-auto" onScroll={(event) => setScrolled(event.currentTarget.scrollLeft > 0)}>
          {rowGroups.map((group) => {
            const selectableIds = group.rows
              .map((treeRow) => treeRow.row.id)
              .filter((id): id is string => typeof id === "string");
            return (
              <table
                aria-label={view.label}
                // WAI-ARIA treegrid pattern for hierarchical data.
                role={isTree ? "treegrid" : undefined}
                key={group.key}
                className="table-fixed border-collapse"
                style={{ width: tableWidth }}
              >
                {/* Pinning width to the colgroup's own sum (vs. leaving it
                    "auto") stops an unbreakable long value in one group's
                    rows from widening that group's table past its
                    <colgroup>, which desyncs cross-group column alignment
                    under the shared scroll container above. */}
                {!isTree && listState.groupBy && (
                  <caption className="bg-bg-subtle p-3 text-left text-sm font-medium text-text-secondary">
                    {listState.groupBy} = {group.key}
                  </caption>
                )}
                {/* <col> widths, not each cell's own inline width, are what
                    actually guarantee every group's independently-rendered
                    <table> lands on identical column widths — this is the
                    one CSS mechanism specifically designed for that,
                    unlike table-layout: fixed's "first row" cell-width
                    heuristic, which held up inconsistently across groups. */}
                <colgroup>
                  {showSelection && <col style={{ width: CHECKBOX_COLUMN_WIDTH }} />}
                  {columns.map((column) => (
                    <col key={column.field} style={{ width: column.width ?? DEFAULT_COLUMN_WIDTH }} />
                  ))}
                </colgroup>
                <thead>
                  <tr className="border-b border-border bg-surface">
                    {showSelection && (
                      <th
                        scope="col"
                        className={`p-3 text-left bg-surface ${stickyCheckboxClassName} ${scrolled ? "shadow-sm" : ""}`}
                      >
                        <input
                          type="checkbox"
                          aria-label={
                            listState.groupBy ? `Select all in ${group.key}` : `Select all ${view.label.toLowerCase()}`
                          }
                          checked={
                            selectableIds.length > 0 && selectableIds.every((id) => selection.selectedIds.has(id))
                          }
                          onChange={() => selection.toggleAll(selectableIds)}
                          style={{ accentColor: "var(--color-primary)" }}
                        />
                      </th>
                    )}
                    {columns.map((column) => {
                      const sortDirection = column.sortable ? sortDirectionOf(listState.sort, column.field) : undefined;
                      return (
                        <th
                          scope="col"
                          key={column.field}
                          style={columnStyle(column)}
                          className="p-3 text-left text-sm font-medium text-text-secondary"
                          aria-sort={
                            !column.sortable
                              ? undefined
                              : sortDirection === undefined
                                ? "none"
                                : sortDirection === "asc"
                                  ? "ascending"
                                  : "descending"
                          }
                        >
                          {column.sortable ? (
                            <button
                              type="button"
                              className="flex w-full items-center gap-2 hover:bg-surface-hover"
                              onClick={() => listState.setSort(nextSortValue(listState.sort, column.field))}
                            >
                              {/* biome-ignore lint/a11y/noStaticElementInteractions: onMouseEnter only lazily computes a native title for the browser's own tooltip — a passive read, not new interactive behavior. */}
                              <span onMouseEnter={handleTruncationHover} style={truncationStyle(column)}>
                                {column.label ?? column.field}
                              </span>
                              {sortDirection === "asc" && <ChevronUp size={14} aria-hidden="true" />}
                              {sortDirection === "desc" && <ChevronDown size={14} aria-hidden="true" />}
                            </button>
                          ) : (
                            // biome-ignore lint/a11y/noStaticElementInteractions: onMouseEnter only lazily computes a native title for the browser's own tooltip — a passive read, not new interactive behavior.
                            <span onMouseEnter={handleTruncationHover} style={truncationStyle(column)}>
                              {column.label ?? column.field}
                            </span>
                          )}
                        </th>
                      );
                    })}
                  </tr>
                </thead>
                <tbody>
                  {group.rows.map((treeRow, index) => {
                    const row = treeRow.row;
                    const selected = typeof row.id === "string" && selection.selectedIds.has(row.id);
                    const rowHandleActivate = wholeRowClickable ? () => navigateToRow(row) : undefined;
                    // Requires a string id — toggleExpand/the children fetch
                    // both need one, and aria-expanded shouldn't announce an
                    // expandable row with nothing to expand it.
                    const showChevron =
                      isTree && typeof row.id === "string" && (treeRow.hasChildrenUnknown || treeRow.hasChildren);
                    const rowKey = (row.id as string | undefined) ?? index;
                    return (
                      <Fragment key={rowKey}>
                        <tr
                          className={`border-b border-border ${selected ? "bg-primary-subtle" : "bg-surface"} ${
                            rowHandleActivate
                              ? "cursor-pointer hover:bg-surface-hover focus-visible:[outline:2px_solid_var(--color-primary)] focus-visible:-outline-offset-2"
                              : ""
                          }`}
                          aria-level={isTree ? treeRow.depth + 1 : undefined}
                          aria-expanded={isTree && showChevron ? treeRow.isExpanded : undefined}
                          tabIndex={rowHandleActivate ? 0 : undefined}
                          onClick={rowHandleActivate}
                          onKeyDown={
                            rowHandleActivate
                              ? (event: KeyboardEvent<HTMLTableRowElement>) => {
                                  if (event.key !== "Enter" && event.key !== " ") return;
                                  event.preventDefault();
                                  rowHandleActivate();
                                }
                              : undefined
                          }
                        >
                          {showSelection && (
                            <td
                              className={`p-3 ${stickyCheckboxClassName} ${selected ? "bg-primary-subtle" : "bg-surface"} ${scrolled ? "shadow-sm" : ""}`}
                            >
                              {typeof row.id === "string" && (
                                <input
                                  type="checkbox"
                                  aria-label="Select row"
                                  checked={selection.selectedIds.has(row.id)}
                                  onChange={() => selection.toggle(row.id as string)}
                                  style={{ accentColor: "var(--color-primary)" }}
                                />
                              )}
                            </td>
                          )}
                          {columns.map((column, columnIndex) => {
                            const rawValue = row[column.field];
                            const relationLabel =
                              typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
                            const content = renderCell(
                              column,
                              row,
                              relationLabel !== undefined ? { relationLabel } : {},
                            );
                            const asRowLink =
                              (showSelection || isTree) &&
                              column.field === primaryColumnField &&
                              !columnRendersOwnLink(column);
                            const href = asRowLink ? rowClickHref(rowClickPath, row, rowClickParam) : undefined;
                            return (
                              <td key={column.field} style={columnStyle(column)} className="p-3 text-base text-text">
                                <span className="flex items-center gap-1">
                                  {isTree && columnIndex === 0 && (
                                    <span
                                      className="inline-flex shrink-0 items-center"
                                      style={{ paddingInlineStart: `calc(var(--space-4) * ${treeRow.depth})` }}
                                    >
                                      {showChevron && typeof row.id === "string" ? (
                                        <button
                                          type="button"
                                          aria-label={treeRow.isExpanded ? "Collapse" : "Expand"}
                                          aria-expanded={treeRow.isExpanded}
                                          onClick={(event) => {
                                            event.stopPropagation();
                                            toggleExpand(row.id as string);
                                          }}
                                          className="inline-flex items-center justify-center rounded-control hover:bg-surface-hover"
                                        >
                                          <ChevronRight
                                            size={14}
                                            aria-hidden="true"
                                            className={`transition-transform duration-(--duration-base) ${treeRow.isExpanded ? "rotate-90" : ""}`}
                                          />
                                        </button>
                                      ) : (
                                        <span aria-hidden="true" className="inline-block size-3.5" />
                                      )}
                                    </span>
                                  )}
                                  {/* biome-ignore lint/a11y/noStaticElementInteractions: onMouseEnter only lazily computes a native title for the browser's own tooltip — a passive read, not new interactive behavior. */}
                                  <span onMouseEnter={handleTruncationHover} style={truncationStyle(column)}>
                                    {href ? <a href={href}>{content}</a> : content}
                                  </span>
                                </span>
                              </td>
                            );
                          })}
                        </tr>
                        {isTree && treeRow.isLoadingChildren && (
                          <tr className="bg-surface">
                            <td colSpan={(showSelection ? 1 : 0) + columns.length} className="p-3">
                              <span
                                className="block"
                                style={{ paddingInlineStart: `calc(var(--space-4) * ${treeRow.depth + 1})` }}
                              >
                                <Skeleton lines={1} />
                              </span>
                            </td>
                          </tr>
                        )}
                        {isTree && treeRow.hasError && !treeRow.isLoadingChildren && (
                          <tr className="bg-surface">
                            <td colSpan={(showSelection ? 1 : 0) + columns.length} className="p-3">
                              <span
                                className="flex items-center gap-2 text-sm text-danger"
                                style={{ paddingInlineStart: `calc(var(--space-4) * ${treeRow.depth + 1})` }}
                              >
                                <Icon name="circle-alert" size={14} aria-hidden="true" />
                                Couldn't load these rows.
                                <button
                                  type="button"
                                  className="font-medium underline hover:no-underline"
                                  onClick={() => typeof row.id === "string" && retryChildren(row.id)}
                                >
                                  Retry
                                </button>
                              </span>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            );
          })}
        </div>
      )}
      {hasNextPage && (
        <div className="flex justify-center p-3">
          <ActionButton
            variant="secondary"
            loading={isFetchingNextPage}
            onClick={() => {
              void fetchNextPage();
            }}
          >
            Load more
          </ActionButton>
        </div>
      )}
    </>
  );
}
