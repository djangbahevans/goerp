import type { FilterParamValue } from "@goerp/sdk";
import { downloadBlob } from "@goerp/sdk";
import { ActionButton, EmptyState, Icon, Skeleton } from "@goerp/sdk/components";
import type { PivotResponse } from "@goerp/sdk/react";
import { usePivotData } from "@goerp/sdk/react";
import type { ReactNode } from "react";
import { ListFilters } from "../list/list-filters.js";
import { useDefaultFilterApplication, useListState } from "../list/use-list-state.js";
import { ViewPage, ViewSurface, ViewToolbar } from "../view-chrome.js";
import { buildPivotSheetData, pivotSheetDataToBlob } from "./pivot-download.js";
import { PivotGrid } from "./pivot-grid.js";
import type { PivotViewDeclaration } from "./pivot-manifest-types.js";
import { mapPivotResponse, toValueColumns } from "./pivot-mapping.js";
import { usePivotWasmData } from "./use-pivot-wasm-data.js";

// shell-architecture.md's PivotRenderer — resolves a manifest "type":
// "pivot" view's rows/columns/values/default_filters/filters/
// allow_download (manifest-spec.md §9.5, view-system.md §8) into the
// PivotGrid primitive's props (goerp#646/#648, picking up where
// PR #755/goerp#718 left off). Built directly on ListRenderer's template —
// same default-filter application, filter state, and embedded-mode
// cache-key isolation.
export interface PivotRendererProps {
  view: PivotViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
}

interface PivotDataSourceProps {
  view: PivotViewDeclaration;
  listState: ReturnType<typeof useListState>;
  filter: Record<string, FilterParamValue>;
  cacheKeyPrefix: string | undefined;
  embedded: boolean | undefined;
}

interface PivotHookResult {
  data: PivotResponse | undefined;
  isLoading: boolean;
  isFetching: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => void;
}

// Shared by both use_wasm branches below — same loading/error/mapped-data/
// download rendering either way, since mapPivotResponse (pivot-mapping.ts)
// normalizes both sources into the same PivotResponse shape first.
function PivotDataView({
  view,
  listState,
  hook,
  embedded,
}: {
  view: PivotViewDeclaration;
  listState: ReturnType<typeof useListState>;
  hook: PivotHookResult;
  embedded: boolean | undefined;
}) {
  const { data, isLoading, isFetching, isError, error, refetch } = hook;
  const page = (children: ReactNode) => (
    <ViewPage embedded={embedded} title={view.label}>
      {children}
    </ViewPage>
  );

  if (isLoading) {
    return page(<Skeleton type="table" columns={view.values.length + 1} />);
  }

  if (isError) {
    return page(
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load {view.label}.</p>
        {error && <p className="text-sm text-text-secondary">{error.message}</p>}
        <ActionButton
          variant="secondary"
          onClick={() => {
            refetch();
          }}
        >
          Retry
        </ActionButton>
      </div>,
    );
  }

  const mapped = data
    ? mapPivotResponse(data, view.rows, view.columns)
    : { rowHeaders: [], columnHeaders: [], cells: [] };
  const valueColumns = toValueColumns(view.values);

  // Exports the full underlying data at leaf granularity — the Download
  // button has no access to PivotGrid's own collapse state (local to
  // that component, never lifted to props), so "the current pivot table"
  // can only mean everything the current filters/rows/columns resolve to,
  // not whatever happens to be visually collapsed at click time.
  async function handleDownload() {
    const sheetData = buildPivotSheetData(
      view.rows,
      mapped.rowHeaders,
      mapped.columnHeaders,
      valueColumns,
      mapped.cells,
    );
    const blob = await pivotSheetDataToBlob(sheetData);
    downloadBlob(blob, `${view.name}.xlsx`);
  }

  return page(
    <ViewSurface>
      <ViewToolbar
        filters={
          <ListFilters
            filters={view.filters ?? []}
            values={listState.filter}
            onChange={listState.setFilter}
            viewName={view.name}
          />
        }
        {...(view.allow_download !== false
          ? {
              controls: (
                <ActionButton
                  variant="secondary"
                  icon="download"
                  onClick={() => {
                    void handleDownload();
                  }}
                >
                  Download
                </ActionButton>
              ),
            }
          : {})}
      />
      <PivotGrid
        rowHeaders={mapped.rowHeaders}
        columnHeaders={mapped.columnHeaders}
        values={valueColumns}
        cells={mapped.cells}
        isRecomputing={isFetching && !isLoading}
        emptyState={<EmptyState title={`No ${view.label.toLowerCase()} data found.`} />}
      />
    </ViewSurface>,
  );
}

// view-system.md §8 "use_wasm: false" — a server-side GROUP BY ROLLUP
// aggregation request on every rows/columns/filter change.
function PivotRendererServer({ view, listState, filter, cacheKeyPrefix, embedded }: PivotDataSourceProps) {
  const { data, isLoading, isFetching, isError, error, refetch } = usePivotData(view.resource, {
    rows: view.rows,
    columns: view.columns,
    values: view.values,
    filter,
    ...(cacheKeyPrefix !== undefined ? { cacheKeyPrefix } : {}),
  });

  return (
    <PivotDataView
      view={view}
      embedded={embedded}
      listState={listState}
      hook={{
        data,
        isLoading,
        isFetching,
        isError,
        error,
        refetch: () => {
          void refetch();
        },
      }}
    />
  );
}

// view-system.md §8 "use_wasm: true" (default) — fetches the full
// filtered dataset as Parquet once and re-aggregates it client-side via
// DuckDB-WASM on every rows/columns/values change, with a fresh fetch
// only when the filter narrows the dataset differently.
function PivotRendererWasm({ view, listState, filter, cacheKeyPrefix, embedded }: PivotDataSourceProps) {
  const { data, isLoading, isError, error, refetch } = usePivotWasmData(view.resource, {
    rows: view.rows,
    columns: view.columns,
    values: view.values,
    filter,
    ...(cacheKeyPrefix !== undefined ? { cacheKeyPrefix } : {}),
  });

  return (
    <PivotDataView
      view={view}
      embedded={embedded}
      listState={listState}
      hook={{ data, isLoading, isFetching: false, isError, error, refetch }}
    />
  );
}

export function PivotRenderer({ view, recordId, embedded, baseFilter }: PivotRendererProps) {
  const listState = useListState(embedded, undefined);

  // rows/columns/values come solely from the manifest, not listState —
  // no need for useDefaultFilterApplication's applySortAndGroupBy option.
  useDefaultFilterApplication(view, listState, embedded);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };
  const cacheKeyPrefix = embedded ? `embedded:${recordId ?? ""}:${view.name}` : undefined;

  // use_wasm is a static manifest field, never toggled at runtime for a
  // mounted view — switching which of these two renders is safe with
  // respect to React's rules of hooks.
  if (view.use_wasm === false) {
    return (
      <PivotRendererServer
        view={view}
        listState={listState}
        filter={filter}
        cacheKeyPrefix={cacheKeyPrefix}
        embedded={embedded}
      />
    );
  }
  return (
    <PivotRendererWasm
      view={view}
      listState={listState}
      filter={filter}
      cacheKeyPrefix={cacheKeyPrefix}
      embedded={embedded}
    />
  );
}
