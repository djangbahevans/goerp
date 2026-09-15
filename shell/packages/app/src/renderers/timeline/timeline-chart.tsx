import { ActionButton, TabPanel, Tabs } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { ReactNode, RefObject } from "react";
import { useEffect, useRef, useState } from "react";
import {
  addDays,
  dateToX,
  formatDateRange,
  gridlineDates,
  gridlineLabel,
  pixelsPerDay,
  startOfDay,
} from "./timeline-date-utils.js";
import { TimelineRow } from "./timeline-row.js";
import {
  ALL_TIMELINE_RANGES,
  type TimelineChartProps,
  type TimelineDragKind,
  type TimelineRangeMode,
  type TimelineRowData,
} from "./timeline-view-types.js";

const RANGE_LABELS: Record<TimelineRangeMode, string> = {
  week: "Week",
  month: "Month",
  quarter: "Quarter",
  year: "Year",
};
const RANGE_ITEMS = ALL_TIMELINE_RANGES.map((mode) => ({ id: mode, label: RANGE_LABELS[mode] }));

const GRIDLINE_HEADER_HEIGHT_PX = 24;

// Measures the track column's real rendered width — a continuous date axis
// needs actual pixels-per-day.
function useTrackWidth(): [RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    // jsdom (the test environment) has no ResizeObserver — degrades to a
    // fixed trackWidth of 0 there rather than crashing every test that
    // renders this component; real browsers always have it.
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = entries[0]?.contentRect.width;
      if (next !== undefined) setWidth(next);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width];
}

interface DateRange {
  start: Date;
  end: Date;
}

function maxDate(a: Date, b: Date): Date {
  return a.getTime() >= b.getTime() ? a : b;
}

function minDate(a: Date, b: Date): Date {
  return a.getTime() <= b.getTime() ? a : b;
}

// "move" shifts both dates by the same delta; a resize edge moves only its
// own date, clamped so it never crosses past the opposite edge.
function applyDelta(kind: TimelineDragKind, base: DateRange, deltaDays: number): DateRange {
  if (kind === "move") return { start: addDays(base.start, deltaDays), end: addDays(base.end, deltaDays) };
  if (kind === "resize-start") {
    return { start: minDate(addDays(base.start, deltaDays), addDays(base.end, -1)), end: base.end };
  }
  return { start: base.start, end: maxDate(addDays(base.end, deltaDays), addDays(base.start, 1)) };
}

function findBarDates(rows: TimelineRowData[], barId: string): DateRange | undefined {
  for (const row of rows) {
    const bar = row.bars.find((b) => b.id === barId);
    if (bar) return { start: bar.start, end: bar.end };
  }
  return undefined;
}

function findBarLabel(rows: TimelineRowData[], barId: string): string {
  for (const row of rows) {
    const bar = row.bars.find((b) => b.id === barId);
    if (bar) return bar.label;
  }
  return "Bar";
}

function withOverrides(rows: TimelineRowData[], overrides: Map<string, DateRange>): TimelineRowData[] {
  if (overrides.size === 0) return rows;
  return rows.map((row) => ({
    ...row,
    bars: row.bars.map((bar) => {
      const override = overrides.get(bar.id);
      return override ? { ...bar, start: override.start, end: override.end } : bar;
    }),
  }));
}

function sameRange(a: DateRange, b: DateRange): boolean {
  return a.start.getTime() === b.start.getTime() && a.end.getTime() === b.end.getTime();
}

// One slot for "what's currently being manipulated" — lets Enter/Escape
// and the projected-position overlay apply to either gesture uniformly.
type ActiveManipulation =
  | {
      mode: "pointer";
      barId: string;
      kind: TimelineDragKind;
      startClientX: number;
      startBar: DateRange;
      projected: DateRange;
    }
  | { mode: "keyboard"; barId: string; original: DateRange; pending: DateRange };

export function TimelineChart({
  rows,
  range,
  rangeMode,
  onRangeModeChange,
  onNavigate,
  allowDrag,
  allowResize,
  onBarChange,
  label,
}: TimelineChartProps): ReactNode {
  const [trackRef, trackWidth] = useTrackWidth();
  const pxPerDay = trackWidth > 0 ? pixelsPerDay(trackWidth, range) : 0;

  const today = startOfDay(new Date());
  const todayInRange = today >= range.start && today <= range.end;
  const gridlines = gridlineDates(range, rangeMode);

  const [active, setActive] = useState<ActiveManipulation | null>(null);
  const [localOverride, setLocalOverride] = useState<Map<string, DateRange>>(new Map());
  const [announcement, setAnnouncement] = useState("");

  // A fresh fetch landed — discard stale overrides (kanban-board.tsx's own
  // groupsProp reconciliation pattern).
  const [prevRows, setPrevRows] = useState(rows);
  if (rows !== prevRows) {
    setPrevRows(rows);
    setLocalOverride(new Map());
  }

  const displayRows = withOverrides(rows, localOverride);

  async function commitChange(barId: string, next: DateRange): Promise<void> {
    const previous = findBarDates(displayRows, barId);
    setLocalOverride((current) => new Map(current).set(barId, next));
    try {
      await onBarChange({ id: barId, ...next });
    } catch (error) {
      // Reverts against the current override map, not a stale closure.
      if (previous) setLocalOverride((current) => new Map(current).set(barId, previous));
      toast.error(`Couldn't move "${findBarLabel(rows, barId)}". Please try again.`);
      console.error("TimelineChart: onBarChange failed", error);
    }
  }

  function handleDragStart(barId: string, kind: TimelineDragKind, clientX: number, current: DateRange): void {
    setActive({ mode: "pointer", barId, kind, startClientX: clientX, startBar: current, projected: current });
  }

  function handleDragMove(clientX: number): void {
    setActive((current) => {
      if (current?.mode !== "pointer" || pxPerDay <= 0) return current;
      const deltaDays = Math.round((clientX - current.startClientX) / pxPerDay);
      return { ...current, projected: applyDelta(current.kind, current.startBar, deltaDays) };
    });
  }

  function handleDragEnd(): void {
    if (active?.mode !== "pointer") return;
    const { barId, projected, startBar } = active;
    setActive(null);
    if (!sameRange(projected, startBar)) void commitChange(barId, projected);
  }

  function handleNudge(barId: string, target: TimelineDragKind, deltaDays: number, current: DateRange): void {
    const isContinuing = active?.mode === "keyboard" && active.barId === barId;
    const original = isContinuing ? active.original : current;
    const base = isContinuing ? active.pending : current;
    const pending = applyDelta(target, base, deltaDays);
    setActive({ mode: "keyboard", barId, original, pending });
    setAnnouncement(`${findBarLabel(rows, barId)} projected ${formatDateRange(pending.start, pending.end)}.`);
  }

  // Commits whichever gesture is active, pointer or keyboard.
  function handleCommit(): void {
    if (!active) return;
    const { barId } = active;
    const next = active.mode === "keyboard" ? active.pending : active.projected;
    const original = active.mode === "keyboard" ? active.original : active.startBar;
    setActive(null);
    setAnnouncement(`${findBarLabel(rows, barId)} moved to ${formatDateRange(next.start, next.end)}.`);
    if (!sameRange(next, original)) void commitChange(barId, next);
  }

  function handleCancel(): void {
    if (!active) return;
    setAnnouncement(`Move cancelled. ${findBarLabel(rows, active.barId)} stays at its previous dates.`);
    setActive(null);
  }

  const activeDrag = active
    ? { barId: active.barId, projected: active.mode === "pointer" ? active.projected : active.pending }
    : undefined;

  return (
    <Tabs items={RANGE_ITEMS} activeId={rangeMode} onChange={(id) => onRangeModeChange(id as TimelineRangeMode)}>
      <TabPanel id={rangeMode}>
        <div className="flex flex-col gap-2">
          <span aria-live="polite" className="sr-only">
            {announcement}
          </span>
          <div className="flex items-center justify-end gap-1">
            <ActionButton variant="ghost" icon="chevron-left" onClick={() => onNavigate(-1)}>
              <span className="sr-only">Previous</span>
            </ActionButton>
            <ActionButton variant="secondary" onClick={() => onNavigate(0)}>
              Today
            </ActionButton>
            <ActionButton variant="ghost" icon="chevron-right" onClick={() => onNavigate(1)}>
              <span className="sr-only">Next</span>
            </ActionButton>
          </div>
          {/* No role="grid"/"row"/"columnheader" — same posture Kanban's
              board/column/card and Calendar's own month grid already take
              (plain elements, individually labeled and focusable, rather
              than a full ARIA grid widget the rest of this codebase
              doesn't otherwise commit to). */}
          {/* biome-ignore lint/a11y/useSemanticElements: role="group" labels
              the chart as a whole ("Timeline: {label}"), not a form's field
              grouping — <fieldset> would be the wrong element here. */}
          <div
            aria-label={`Timeline: ${label}`}
            role="group"
            className="relative grid grid-cols-[128px_1fr] gap-x-3 gap-y-2"
          >
            <div className="contents">
              <div />
              <div ref={trackRef} className="relative" style={{ height: GRIDLINE_HEADER_HEIGHT_PX }}>
                {gridlines.map((date) => (
                  <span
                    key={date.toISOString()}
                    className="absolute text-text-secondary text-xs"
                    style={{ left: dateToX(date, range, pxPerDay) }}
                  >
                    {gridlineLabel(date, rangeMode)}
                  </span>
                ))}
              </div>
            </div>
            {todayInRange && pxPerDay > 0 && (
              <div aria-hidden="true" className="relative col-start-2 row-span-full row-start-1">
                <div
                  className="pointer-events-none absolute top-0 bottom-0 w-px bg-primary"
                  style={{ left: dateToX(today, range, pxPerDay) }}
                />
              </div>
            )}
            {displayRows.map((row) => (
              <TimelineRow
                key={row.id}
                row={row}
                range={range}
                pxPerDay={pxPerDay}
                allowDrag={allowDrag}
                allowResize={allowResize}
                activeDrag={activeDrag}
                onBarDragStart={handleDragStart}
                onBarDragMove={handleDragMove}
                onBarDragEnd={handleDragEnd}
                onBarNudge={handleNudge}
                onBarCommit={handleCommit}
                onBarCancel={handleCancel}
              />
            ))}
          </div>
        </div>
      </TabPanel>
    </Tabs>
  );
}
