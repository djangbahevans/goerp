import type { RelationBatchSpec } from "@goerp/sdk/react";
import { useInfiniteList, useRelationLabels } from "@goerp/sdk/react";
import { renderCell } from "./column-renderers.js";
import { ListActions } from "./list-actions.js";
import { ListFilters } from "./list-filters.js";
import type { ListColumn, ListViewDeclaration } from "./list-view-types.js";
import { useListState } from "./use-list-state.js";
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

type Row = Record<string, unknown>;

function defaultSortOf(view: ListViewDeclaration): string | undefined {
  if (!view.default_sort) return undefined;
  return view.default_sort_dir === "desc" ? `-${view.default_sort}` : view.default_sort;
}

// Relation columns resolved via the batch-fetch fallback (no `display_field`,
// an explicit `resource_label_field` given) — the auto-default-from-registry
// case needs the model/view registry goerp#636 deferred to backlog #674.
function relationBatchSpecs(columns: ListColumn[], rows: Row[]): RelationBatchSpec[] {
  return columns
    .filter((c) => c.type === "relation" && !c.display_field && c.resource && c.resource_label_field)
    .map((c) => ({
      key: c.field,
      resource: c.resource as string,
      labelField: c.resource_label_field as string,
      ids: rows.map((row) => row[c.field]).filter((value): value is string => typeof value === "string"),
    }));
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

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };

  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading, isError, error, refetch } =
    useInfiniteList<Row>(view.resource, {
      filter,
      ...(listState.sort !== undefined ? { sort: listState.sort } : {}),
      ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
    });

  const rows = data?.pages.flatMap((page) => page.data) ?? [];
  const relationLabels = useRelationLabels(relationBatchSpecs(columns, rows));

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
        groupRows(rows, listState.groupBy).map((group) => (
          <table aria-label={view.label} key={group.key}>
            {group.key && (
              <caption>
                {listState.groupBy} = {group.key}
              </caption>
            )}
            <thead>
              <tr>
                {columns.map((column) => (
                  <th scope="col" key={column.field}>
                    {column.label ?? column.field}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {group.rows.map((row, index) => (
                <tr key={(row.id as string | undefined) ?? index}>
                  {columns.map((column) => {
                    const rawValue = row[column.field];
                    const relationLabel =
                      typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
                    return (
                      <td key={column.field}>
                        {renderCell(column, row, relationLabel !== undefined ? { relationLabel } : {})}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        ))
      )}
      {hasNextPage && (
        <button type="button" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
          {isFetchingNextPage ? "Loading…" : "Load more"}
        </button>
      )}
    </>
  );
}
