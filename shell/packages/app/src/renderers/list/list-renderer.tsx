import { useInfiniteList } from "@goerp/sdk/react";
import type { ListViewDeclaration } from "./list-view-types.js";
import { useListState } from "./use-list-state.js";
import { useVisibleColumns } from "./use-visible-columns.js";

// shell-architecture.md §20's ListRenderer — mode switching, URL/local
// state, field security. Column/filter/action rendering is goerp#575's scope.
export interface ListRendererProps {
  view: ListViewDeclaration;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
}

type Row = Record<string, unknown>;

function defaultSortOf(view: ListViewDeclaration): string | undefined {
  if (!view.default_sort) return undefined;
  return view.default_sort_dir === "desc" ? `-${view.default_sort}` : view.default_sort;
}

export function ListRenderer({ view, recordId, embedded, baseFilter }: ListRendererProps) {
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
        <button type="button" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
          {isFetchingNextPage ? "Loading…" : "Load more"}
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
