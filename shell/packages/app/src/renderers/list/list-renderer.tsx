import { useInfiniteList } from "@goerp/sdk/react";
import type { ListViewDeclaration } from "./list-view-types.js";
import { useListState } from "./use-list-state.js";
import { useVisibleColumns } from "./use-visible-columns.js";

// shell-architecture.md §20's ListRenderer — mode switching, URL/local
// state, and field security. Column-type-specific cell rendering,
// filters, actions, and groups are goerp#575's own scope: this component
// renders each visible column's raw field value and delegates real
// row/column content to whatever #575 provides later.
export interface ListRendererProps {
  view: ListViewDeclaration;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
}

type Row = Record<string, unknown>;

export function ListRenderer({ view, embedded, baseFilter }: ListRendererProps) {
  const listState = useListState(embedded, view.default_sort);
  const columns = useVisibleColumns(view);

  const filter = { ...baseFilter, ...listState.filter };

  const { data, fetchNextPage, hasNextPage, isFetching, isLoading, isError, error, refetch } = useInfiniteList<Row>(
    view.resource,
    { filter, ...(listState.sort !== undefined ? { sort: listState.sort } : {}) },
  );

  const rows = data?.pages.flatMap((page) => page.data) ?? [];

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

  if (rows.length === 0) {
    return <div role="status">No {view.label.toLowerCase()} found.</div>;
  }

  return (
    <>
      <table aria-label={view.label}>
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
          {rows.map((row, index) => (
            <tr key={(row.id as string | undefined) ?? index}>
              {columns.map((column) => (
                <td key={column.field}>{formatCell(row[column.field])}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {hasNextPage && (
        <button type="button" onClick={() => fetchNextPage()} disabled={isFetching}>
          {isFetching ? "Loading…" : "Load more"}
        </button>
      )}
    </>
  );
}

function formatCell(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  return String(value);
}
