import { ActionButton, Icon, Skeleton } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { toast } from "@goerp/sdk/notifications";
import { useInfiniteList } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { ListFilters } from "../list/list-filters.js";
import type { Row } from "../list/list-view-types.js";
import { computeDefaultFilters, useListState } from "../list/use-list-state.js";
import { dateKey, visibleRange } from "./calendar-date-utils.js";
import { buildCalendarEvents, initialViewMode } from "./calendar-events.js";
import type { CalendarViewDeclaration } from "./calendar-manifest-types.js";
import { CalendarView } from "./calendar-view.js";
import type { CalendarEvent } from "./calendar-view-types.js";

// view-system.md §7's GenericCalendarRenderer — resolves a manifest
// "type": "calendar" view (manifest-spec.md §9.4) into CalendarView's props.
// Built directly on ListRenderer's template — same default-filter
// application, filter state, and embedded-mode cache-key isolation.
export interface CalendarRendererProps {
  view: CalendarViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
  // Threaded straight through to CalendarView's own initialDate — fixes
  // "today" and the initial fetch range for deterministic stories/tests.
  initialDate?: Date;
}

function useResolvedPath(viewName: string | undefined, module: string): string | null {
  const [path, setPath] = useState<string | null>(null);
  useEffect(() => {
    if (!viewName) {
      setPath(null);
      return;
    }
    let cancelled = false;
    viewPathRegistry
      .resolve(viewName, module)
      .then((resolved) => {
        if (!cancelled) setPath(resolved);
      })
      .catch(() => {
        if (!cancelled) setPath(null);
      });
    return () => {
      cancelled = true;
    };
  }, [viewName, module]);
  return path;
}

export function CalendarRenderer({ view, module, recordId, embedded, baseFilter, initialDate }: CalendarRendererProps) {
  const listState = useListState(embedded, undefined);
  const navigate = useNavigate();

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

  // Computed once and passed to CalendarView's own initialDate below,
  // rather than each calling `new Date()` independently — two separate
  // calls straddling local midnight would disagree on "today", desyncing
  // this renderer's first fetch range from what CalendarView actually
  // shows until its mount effect reports the real range.
  const [today] = useState(() => initialDate ?? new Date());
  const [range, setRange] = useState(() => visibleRange(today, initialViewMode(view)));

  const onClickPath = useResolvedPath(view.on_click, module);
  const onDateClickPath = useResolvedPath(view.on_date_click, module);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around — so it
  // spreads last, even over the computed date-range bound.
  const filter = {
    ...listState.filter,
    [view.date_field]: { gte: dateKey(range.start), lte: dateKey(range.end) },
    ...baseFilter,
  };

  const { data, isLoading, isError, error, refetch } = useInfiniteList<Row>(view.resource, {
    filter,
    ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
  });

  const rows = useMemo(() => (data?.pages.map((page) => page.data) ?? []).flat(), [data]);
  const events = useMemo(() => buildCalendarEvents(rows, view), [rows, view]);

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

  function handleEventClick(event: CalendarEvent): void {
    if (!onClickPath) return;
    void navigate({ to: moduleLink(onClickPath.replace("{id}", event.id)) });
  }

  // Passes the clicked date as a `?{date_field}=` query param — best-effort
  // for a target form that wants to pre-fill it; harmless if it doesn't.
  function handleQuickCreate(date: Date): void {
    if (!onDateClickPath) {
      toast.error(`"${view.on_date_click}" doesn't resolve to a route yet.`);
      return;
    }
    void navigate({ to: moduleLink(`${onDateClickPath}?${view.date_field}=${dateKey(date)}`) });
  }

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      <CalendarView
        events={events}
        {...(view.allowed_views ? { allowedViews: view.allowed_views } : {})}
        {...(view.default_view ? { defaultView: view.default_view } : {})}
        initialDate={today}
        quickCreate={Boolean(view.quick_create) && view.on_date_click !== undefined}
        onEventClick={handleEventClick}
        onQuickCreate={handleQuickCreate}
        onVisibleRangeChange={setRange}
      />
    </>
  );
}
