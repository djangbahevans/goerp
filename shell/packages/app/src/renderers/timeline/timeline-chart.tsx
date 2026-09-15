import { ActionButton, TabPanel, Tabs } from "@goerp/sdk/components";
import type { ReactNode, RefObject } from "react";
import { useEffect, useRef, useState } from "react";
import { dateToX, gridlineDates, gridlineLabel, pixelsPerDay, startOfDay } from "./timeline-date-utils.js";
import { TimelineRow } from "./timeline-row.js";
import { ALL_TIMELINE_RANGES, type TimelineChartProps, type TimelineRangeMode } from "./timeline-view-types.js";

const RANGE_LABELS: Record<TimelineRangeMode, string> = {
  week: "Week",
  month: "Month",
  quarter: "Quarter",
  year: "Year",
};
const RANGE_ITEMS = ALL_TIMELINE_RANGES.map((mode) => ({ id: mode, label: RANGE_LABELS[mode] }));

const GRIDLINE_HEADER_HEIGHT_PX = 24;

// Measures the track column's real rendered width — a continuous date axis
// needs actual pixels-per-day, unlike every sibling renderer's fixed or
// CSS-grid-only layout (pivot-grid.tsx explicitly opts out of this for its
// own simpler fixed-width column).
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

export function TimelineChart({
  rows,
  range,
  rangeMode,
  onRangeModeChange,
  onNavigate,
  allowDrag,
  allowResize,
  label,
}: TimelineChartProps): ReactNode {
  const [trackRef, trackWidth] = useTrackWidth();
  const pxPerDay = trackWidth > 0 ? pixelsPerDay(trackWidth, range) : 0;

  const today = startOfDay(new Date());
  const todayInRange = today >= range.start && today <= range.end;
  const gridlines = gridlineDates(range, rangeMode);

  return (
    <Tabs items={RANGE_ITEMS} activeId={rangeMode} onChange={(id) => onRangeModeChange(id as TimelineRangeMode)}>
      <TabPanel id={rangeMode}>
        <div className="flex flex-col gap-2">
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
            {rows.map((row) => (
              <TimelineRow
                key={row.id}
                row={row}
                range={range}
                pxPerDay={pxPerDay}
                allowDrag={allowDrag}
                allowResize={allowResize}
              />
            ))}
          </div>
        </div>
      </TabPanel>
    </Tabs>
  );
}
