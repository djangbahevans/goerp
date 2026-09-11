import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { addDays, dateKey, eventsOnDay, isSameDay } from "./calendar-date-utils.js";
import type { CalendarEvent } from "./calendar-view-types.js";
import { EventChip } from "./event-chip.js";
import { QuickCreatePopover } from "./quick-create-popover.js";

export interface CalendarTimeGridProps {
  days: Date[];
  focusedDate: Date;
  onFocusedDateChange: (date: Date) => void;
  events: CalendarEvent[];
  quickCreate?: boolean | undefined;
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
  onQuickCreate?: ((date: Date) => void) | undefined;
}

const HOURS = Array.from({ length: 24 }, (_, hour) => hour);
const ROW_HEIGHT_PX = 48;
const HOUR_FORMAT = new Intl.DateTimeFormat(undefined, { hour: "numeric" });
const DAY_HEADER_FORMAT = new Intl.DateTimeFormat(undefined, { weekday: "short", day: "numeric" });
const FULL_DATE_FORMAT = new Intl.DateTimeFormat(undefined, { dateStyle: "full" });

function minutesFromMidnight(date: Date): number {
  return date.getHours() * 60 + date.getMinutes();
}

// An event is "all-day" for this grid's purposes if it has no end (a
// point-in-time event still renders in its own hour slot, not the all-day
// row) — otherwise it's all-day/multi-day when its span crosses a whole
// day or more, per the documented "multi-day event is a case of the same
// [all-day] row" convention.
function isAllDayOrMultiDay(event: CalendarEvent): boolean {
  if (!event.end) return false;
  return !isSameDay(event.start, event.end) || event.end.getTime() - event.start.getTime() >= 24 * 60 * 60 * 1000;
}

interface TimedLayout {
  event: CalendarEvent;
  topPx: number;
  heightPx: number;
  lane: number;
  laneCount: number;
}

// Greedy interval-graph layout for side-by-side overlapping events
// (view-system.md's Open Questions flags this as manifest-unaddressed).
// Sweeps into transitively-overlapping clusters and shares one lane count
// per cluster, so concurrent events always divide the same total width.
function layoutTimedEvents(dayEvents: CalendarEvent[]): TimedLayout[] {
  const timed = dayEvents
    .filter((event) => event.end && !isAllDayOrMultiDay(event))
    .map((event) => ({ event, start: event.start.getTime(), end: (event.end as Date).getTime() }))
    .sort((a, b) => a.start - b.start);

  const results: TimedLayout[] = [];
  let clusterStart = 0;

  while (clusterStart < timed.length) {
    let clusterEnd = clusterStart + 1;
    let runningMaxEnd = (timed[clusterStart] as (typeof timed)[number]).end;
    while (clusterEnd < timed.length && (timed[clusterEnd] as (typeof timed)[number]).start < runningMaxEnd) {
      runningMaxEnd = Math.max(runningMaxEnd, (timed[clusterEnd] as (typeof timed)[number]).end);
      clusterEnd++;
    }

    const laneEnds: number[] = [];
    const lanes: number[] = [];
    for (let i = clusterStart; i < clusterEnd; i++) {
      const item = timed[i] as (typeof timed)[number];
      let lane = laneEnds.findIndex((laneEnd) => laneEnd <= item.start);
      if (lane === -1) lane = laneEnds.length;
      laneEnds[lane] = item.end;
      lanes.push(lane);
    }
    const laneCount = laneEnds.length;

    for (let i = clusterStart; i < clusterEnd; i++) {
      const item = timed[i] as (typeof timed)[number];
      const startMinutes = minutesFromMidnight(item.event.start);
      const endMinutes = minutesFromMidnight(item.event.end as Date);
      results.push({
        event: item.event,
        lane: lanes[i - clusterStart] as number,
        laneCount,
        topPx: (startMinutes / 60) * ROW_HEIGHT_PX,
        heightPx: Math.max(((endMinutes - startMinutes) / 60) * ROW_HEIGHT_PX, ROW_HEIGHT_PX / 2),
      });
    }

    clusterStart = clusterEnd;
  }

  return results;
}

function pointInTimeEvents(dayEvents: CalendarEvent[]): CalendarEvent[] {
  return dayEvents.filter((event) => !event.end);
}

interface FocusedSlot {
  dateKey: string;
  hour: number;
}

export function CalendarTimeGrid({
  days,
  focusedDate,
  onFocusedDateChange,
  events,
  quickCreate = false,
  onEventClick,
  onQuickCreate,
}: CalendarTimeGridProps): ReactNode {
  const [focusedHour, setFocusedHour] = useState(focusedDate.getHours());
  const [quickCreateSlot, setQuickCreateSlot] = useState<Date | null>(null);
  const slotRefs = useRef(new Map<string, HTMLButtonElement>());
  const shouldFocusSlot = useRef(false);

  const focused: FocusedSlot = { dateKey: dateKey(focusedDate), hour: focusedHour };

  useEffect(() => {
    if (!shouldFocusSlot.current) return;
    shouldFocusSlot.current = false;
    slotRefs.current.get(`${focused.dateKey}:${focused.hour}`)?.focus();
  }, [focused.dateKey, focused.hour]);

  function moveTo(day: Date, hour: number): void {
    shouldFocusSlot.current = true;
    setFocusedHour(hour);
    onFocusedDateChange(day);
  }

  function activateSlot(day: Date, hour: number): void {
    const slotStart = new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour);
    const slotEvents = eventsOnDay(events, day).filter(
      (event) => !isAllDayOrMultiDay(event) && event.start.getHours() === hour,
    );
    if (slotEvents.length === 0) {
      if (quickCreate) setQuickCreateSlot(slotStart);
      return;
    }
    onEventClick?.(slotEvents[0] as CalendarEvent);
  }

  function handleSlotKeyDown(event: KeyboardEvent<HTMLButtonElement>, day: Date, hour: number, dayIndex: number): void {
    switch (event.key) {
      case "ArrowUp":
        event.preventDefault();
        if (hour > 0) moveTo(day, hour - 1);
        break;
      case "ArrowDown":
        event.preventDefault();
        if (hour < 23) moveTo(day, hour + 1);
        break;
      case "ArrowLeft":
        event.preventDefault();
        moveTo(dayIndex > 0 ? (days[dayIndex - 1] as Date) : addDays(day, -1), hour);
        break;
      case "ArrowRight":
        event.preventDefault();
        moveTo(dayIndex < days.length - 1 ? (days[dayIndex + 1] as Date) : addDays(day, 1), hour);
        break;
      case "Home":
        event.preventDefault();
        moveTo(day, 0);
        break;
      case "End":
        event.preventDefault();
        moveTo(day, 23);
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        activateSlot(day, hour);
        break;
      default:
        break;
    }
  }

  const allDayEvents = events.filter(isAllDayOrMultiDay);
  const gridTemplateColumns = `4rem repeat(${days.length}, 1fr)`;

  return (
    <div className="flex flex-col">
      {allDayEvents.length > 0 && (
        <div className="grid border-border border-b" style={{ gridTemplateColumns }}>
          <div className="p-1 text-text-secondary text-xs">All day</div>
          {days.map((day) => (
            <div key={dateKey(day)} className="flex flex-col gap-0.5 border-border border-l p-1">
              {allDayEvents
                .filter((event) => isSameDay(event.start, day) || (event.end && event.start < day && event.end >= day))
                .map((event) => (
                  <EventChip key={event.id} event={event} onClick={onEventClick} />
                ))}
            </div>
          ))}
        </div>
      )}
      {/* Kept outside the hour table: the event overlay's pixel-precise
          positions assume the table starts at 12 AM, and a <thead> row
          inside it would offset everything by its own height. */}
      <div className="grid border-border border-b" style={{ gridTemplateColumns }}>
        <div />
        {days.map((day) => (
          <div key={dateKey(day)} className="p-2 text-center text-sm text-text">
            {DAY_HEADER_FORMAT.format(day)}
          </div>
        ))}
      </div>
      {/* A real <table>, no role="grid" (biome's a11y linter refuses it on
          a non-interactive element). Event blocks span several hour rows,
          so they render as a separate absolutely-positioned overlay below
          rather than living inside individual <td> cells. */}
      <div className="relative">
        <table aria-label="Schedule" className="w-full table-fixed border-collapse">
          <colgroup>
            <col className="w-16" />
            {days.map((day) => (
              <col key={dateKey(day)} />
            ))}
          </colgroup>
          <tbody>
            {HOURS.map((hour) => (
              <tr key={hour}>
                <td
                  style={{ height: ROW_HEIGHT_PX }}
                  className="border-border border-b pr-2 text-right text-text-secondary text-xs align-top"
                >
                  {HOUR_FORMAT.format(new Date(2026, 0, 1, hour))}
                </td>
                {days.map((day, dayIndex) => {
                  const isFocused = isSameDay(day, focusedDate) && hour === focusedHour;
                  return (
                    <td
                      key={dateKey(day)}
                      style={{ height: ROW_HEIGHT_PX }}
                      className="border-border border-b border-l p-0"
                    >
                      <button
                        type="button"
                        style={{ height: ROW_HEIGHT_PX }}
                        tabIndex={isFocused ? 0 : -1}
                        ref={(el) => {
                          const refKey = `${dateKey(day)}:${hour}`;
                          if (el) slotRefs.current.set(refKey, el);
                          else slotRefs.current.delete(refKey);
                        }}
                        aria-label={`${FULL_DATE_FORMAT.format(day)}, ${HOUR_FORMAT.format(new Date(2026, 0, 1, hour))}`}
                        onClick={() => {
                          moveTo(day, hour);
                          activateSlot(day, hour);
                        }}
                        onKeyDown={(event) => handleSlotKeyDown(event, day, hour, dayIndex)}
                        className="block w-full focus-visible:shadow-focus"
                      />
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
        <div className="pointer-events-none absolute inset-0 grid" style={{ gridTemplateColumns }}>
          <div />
          {days.map((day) => {
            const dayEvents = eventsOnDay(events, day).filter((event) => !isAllDayOrMultiDay(event));
            const timedLayout = layoutTimedEvents(dayEvents);
            const pointEvents = pointInTimeEvents(dayEvents);

            return (
              <div key={dateKey(day)} className="relative">
                {/* Ordinary Tab stops here, not the roving-tabindex nested
                    chips month-grid builds — that "second level" nesting
                    isn't part of this view's AC. */}
                {timedLayout.map(({ event, topPx, heightPx, lane, laneCount }) => (
                  <div
                    key={event.id}
                    className="pointer-events-auto absolute px-0.5"
                    style={{
                      top: topPx,
                      height: heightPx,
                      left: `${(lane / laneCount) * 100}%`,
                      width: `${100 / laneCount}%`,
                    }}
                  >
                    <EventChip event={event} onClick={onEventClick} />
                  </div>
                ))}
                {pointEvents.map((event) => (
                  <div
                    key={event.id}
                    className="pointer-events-auto absolute px-0.5"
                    style={{ top: (minutesFromMidnight(event.start) / 60) * ROW_HEIGHT_PX, width: "100%" }}
                  >
                    <EventChip event={event} onClick={onEventClick} />
                  </div>
                ))}
                {quickCreateSlot && isSameDay(quickCreateSlot, day) && (
                  <div
                    className="pointer-events-auto absolute"
                    style={{ top: (minutesFromMidnight(quickCreateSlot) / 60) * ROW_HEIGHT_PX }}
                  >
                    <QuickCreatePopover
                      date={quickCreateSlot}
                      onCreate={() => {
                        onQuickCreate?.(quickCreateSlot);
                        setQuickCreateSlot(null);
                      }}
                      onClose={() => setQuickCreateSlot(null)}
                      triggerRef={{
                        current: slotRefs.current.get(`${dateKey(day)}:${quickCreateSlot.getHours()}`) ?? null,
                      }}
                    />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
