import { downloadBlob } from "@goerp/sdk";
import { ActionButton, EmptyState, Icon, Skeleton } from "@goerp/sdk/components";
import { usePivotData, useSavedFilters } from "@goerp/sdk/react";
import { useEffect, useRef } from "react";
import { ListFilters } from "../list/list-filters.js";
import { computeDefaultFilters, resolveDefaultSavedFilterState } from "../list/list-renderer.js";
import { useListState } from "../list/use-list-state.js";
import { buildPivotSheetData, pivotSheetDataToBlob } from "./pivot-download.js";
import type { PivotViewDeclaration } from "./pivot-manifest-types.js";
import { mapPivotResponse, toValueColumns } from "./pivot-mapping.js";
import { PivotView } from "./pivot-view.js";

// shell-architecture.md's PivotRenderer — resolves a manifest "type":
// "pivot" view's rows/columns/values/default_filters/filters/
// allow_download (manifest-spec.md §9.5, view-system.md §8's "use_wasm:
// false" path) into the PivotView/PivotGrid primitive's props (goerp#646,
// picking up where PR #755/goerp#718 left off). Built directly on
// ListRenderer's template — same default-filter application, filter
// state, and embedded-mode cache-key isolation.
export interface PivotRendererProps {
  view: PivotViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
}

export function PivotRenderer({ view, recordId, embedded, baseFilter }: PivotRendererProps) {
  const listState = useListState(embedded, undefined);

  // Embedded views never touch the URL, so the "explicit URL params"
  // precedence tier doesn't apply to them, and there's nothing to fetch —
  // disabled rather than wasting a request whose result is never used.
  const savedFiltersView = useSavedFilters(view.name, { enabled: !embedded });

  // Snapshotted once at mount, not read live inside the effect below —
  // the effect's own run is deferred until savedFiltersView resolves, and
  // re-reading listState.filter live at that later point can no longer
  // distinguish "never had anything" from "the user already cleared it
  // back to empty" during the wait. Pivot never reads listState.sort/
  // groupBy (rows/columns/values come solely from the manifest), so
  // unlike ListRenderer this only ever needs to guard filter.
  const hadExplicitFilterOnMount = useRef(Object.keys(listState.filter).length > 0);

  // Applied once, waiting for savedFiltersView before applying either
  // kind of default (view-system.md §4's precedence rule), so a user's
  // own is_default saved filter can beat the manifest's default_filters
  // rather than racing it.
  const defaultsApplied = useRef(false);
  const { setFilters } = listState;
  // biome-ignore lint/correctness/useExhaustiveDependencies: guarded by defaultsApplied, gated on savedFiltersView.isLoading; view/setFilters/savedFiltersView.filters/hadExplicitFilterOnMount are deliberately read only once that gate opens, not tracked as change-triggers.
  useEffect(() => {
    if (defaultsApplied.current) return;
    if (!embedded && savedFiltersView.isLoading) return;
    defaultsApplied.current = true;
    if (hadExplicitFilterOnMount.current) return;

    const savedDefault = embedded ? undefined : resolveDefaultSavedFilterState(savedFiltersView.filters);
    if (savedDefault) {
      if (Object.keys(savedDefault.filter).length > 0) setFilters(savedDefault.filter);
      return;
    }

    const defaults = computeDefaultFilters(view);
    if (Object.keys(defaults).length > 0) setFilters(defaults);
  }, [embedded, savedFiltersView.isLoading]);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };

  const { data, isLoading, isFetching, isError, error, refetch } = usePivotData(view.resource, {
    rows: view.rows,
    columns: view.columns,
    values: view.values,
    filter,
    ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
  });

  if (isLoading) {
    return <Skeleton type="table" columns={view.values.length + 1} />;
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

  const mapped = data
    ? mapPivotResponse(data, view.rows, view.columns)
    : { rowHeaders: [], columnHeaders: [], cells: [] };
  const valueColumns = toValueColumns(view.values);

  // Exports the full underlying data at leaf granularity — PivotView's
  // onDownload has no access to PivotGrid's own collapse state (local to
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

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      <PivotView
        title={view.label}
        rowHeaders={mapped.rowHeaders}
        columnHeaders={mapped.columnHeaders}
        values={valueColumns}
        cells={mapped.cells}
        isRecomputing={isFetching && !isLoading}
        allowDownload={view.allow_download}
        onDownload={() => {
          void handleDownload();
        }}
        emptyState={<EmptyState title={`No ${view.label.toLowerCase()} data found.`} />}
      />
    </>
  );
}
