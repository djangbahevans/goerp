import { ActionButton, Icon, Skeleton } from "@goerp/sdk/components";
import { createInfiniteListQueryOptions, saveRecord, useInfiniteList } from "@goerp/sdk/react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { ListFilters } from "../list/list-filters.js";
import type { Row } from "../list/list-view-types.js";
import { computeDefaultFilters, useListState } from "../list/use-list-state.js";
import { TimelineChart } from "./timeline-chart.js";
import { dateKey, initialRangeMode, navigateRange, startOfDay, visibleRange } from "./timeline-date-utils.js";
import { buildTimelineRows } from "./timeline-layout.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import { buildOverlapFilter } from "./timeline-overlap-filter.js";
import type { TimelineBarChange, TimelineRangeMode } from "./timeline-view-types.js";

// view-system.md's GenericTimelineRenderer — resolves a manifest "type":
// "timeline" view (manifest-spec.md §9.6) into TimelineChart's props.
// Built directly on ListRenderer's template — same default-filter
// application, filter state, and embedded-mode cache-key isolation Kanban
// and Calendar already follow.
export interface TimelineRendererProps {
  view: TimelineViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
  // Fixes "today" and the initial fetch range for deterministic
  // stories/tests — same role as CalendarRenderer's own initialDate.
  initialDate?: Date;
}

export function TimelineRenderer({ view, recordId, embedded, baseFilter, initialDate }: TimelineRendererProps) {
  const listState = useListState(embedded, undefined);
  const queryClient = useQueryClient();

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

  const [today] = useState(() => initialDate ?? new Date());
  const [focusedDate, setFocusedDate] = useState(today);
  const [rangeMode, setRangeMode] = useState<TimelineRangeMode>(() => initialRangeMode(view));
  const range = useMemo(() => visibleRange(focusedDate, rangeMode), [focusedDate, rangeMode]);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around — so it
  // spreads last, even over the computed overlap-range bound.
  const filter = {
    ...listState.filter,
    ...buildOverlapFilter(view, range),
    ...baseFilter,
  };

  const infiniteListOptions = {
    filter,
    ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
  };
  const { data, isLoading, isError, error, refetch } = useInfiniteList<Row>(view.resource, infiniteListOptions);
  const { queryKey } = createInfiniteListQueryOptions<Row>(view.resource, infiniteListOptions);

  const rows = useMemo(() => (data?.pages.map((page) => page.data) ?? []).flat(), [data]);
  const timelineRows = useMemo(() => buildTimelineRows(rows, view), [rows, view]);

  if (isLoading) {
    return <Skeleton type="card" />;
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

  function handleNavigate(direction: -1 | 0 | 1): void {
    setFocusedDate(direction === 0 ? startOfDay(new Date()) : navigateRange(focusedDate, rangeMode, direction));
  }

  async function handleBarChange(change: TimelineBarChange): Promise<void> {
    await saveRecord(view.resource, change.id, {
      [view.start_field]: dateKey(change.start),
      [view.end_field]: dateKey(change.end),
    });
    void queryClient.invalidateQueries({ queryKey });
  }

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      <TimelineChart
        rows={timelineRows}
        range={range}
        rangeMode={rangeMode}
        onRangeModeChange={setRangeMode}
        onNavigate={handleNavigate}
        allowDrag={view.allow_drag ?? true}
        allowResize={view.allow_resize ?? true}
        onBarChange={handleBarChange}
        label={view.label}
      />
    </>
  );
}
