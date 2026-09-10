import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import {
  addDays,
  addMonthsKeepingWeekday,
  dateKey,
  eventsOnDay,
  isInMonth,
  isSameDay,
  monthGridDays,
  startOfMonth,
  startOfWeek,
} from "./calendar-date-utils.js";
import type { CalendarEvent } from "./calendar-view-types.js";
import { EventChip } from "./event-chip.js";
import { FLOATING_PANEL_CLASSES, QuickCreatePopover } from "./quick-create-popover.js";

export interface CalendarMonthGridProps {
  focusedDate: Date;
  onFocusedDateChange: (date: Date) => void;
  today: Date;
  events: CalendarEvent[];
  quickCreate?: boolean | undefined;
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
  onQuickCreate?: ((date: Date) => void) | undefined;
}

const MAX_STACKED_CHIPS = 3;
const WEEKDAY_LABELS = Array.from({ length: 7 }, (_, i) =>
  new Intl.DateTimeFormat(undefined, { weekday: "short" }).format(new Date(2026, 3, 5 + i)),
);
const FULL_DATE_FORMAT = new Intl.DateTimeFormat(undefined, { dateStyle: "full" });
const LONG_DATE_FORMAT = new Intl.DateTimeFormat(undefined, { dateStyle: "long" });

interface ChipFocus {
  dateKey: string;
  // Index into the day's *displayed* items: 0..MAX_STACKED_CHIPS-1 for a
  // visible chip, MAX_STACKED_CHIPS for the "+N more" button when present.
  index: number;
}

export function CalendarMonthGrid({
  focusedDate,
  onFocusedDateChange,
  today,
  events,
  quickCreate = false,
  onEventClick,
  onQuickCreate,
}: CalendarMonthGridProps): ReactNode {
  const [chipFocus, setChipFocus] = useState<ChipFocus | null>(null);
  const [quickCreateDate, setQuickCreateDate] = useState<Date | null>(null);
  const [overflowDay, setOverflowDay] = useState<Date | null>(null);
  const cellRefs = useRef(new Map<string, HTMLButtonElement>());
  const chipRefs = useRef(new Map<string, HTMLElement>());
  // Guards against stealing focus on mount/prop-driven re-renders — only a
  // keyboard/click interaction inside this grid should move DOM focus,
  // matching ActionMenu's own open-driven-effect convention.
  const shouldFocusCell = useRef(false);
  const shouldFocusChip = useRef(false);

  const monthStart = startOfMonth(focusedDate);
  const days = monthGridDays(monthStart);

  useEffect(() => {
    if (!shouldFocusCell.current) return;
    shouldFocusCell.current = false;
    cellRefs.current.get(dateKey(focusedDate))?.focus();
  }, [focusedDate]);

  useEffect(() => {
    if (!chipFocus || !shouldFocusChip.current) return;
    shouldFocusChip.current = false;
    chipRefs.current.get(`${chipFocus.dateKey}:${chipFocus.index}`)?.focus();
  }, [chipFocus]);

  function moveTo(day: Date): void {
    setChipFocus(null);
    shouldFocusCell.current = true;
    onFocusedDateChange(day);
  }

  // Shared by click and Enter/Space — "Enter/Space on a focused day
  // activates it exactly as a click would" (view-system.md's month-grid
  // Accessibility section takes click as the primitive both describe).
  function activateDay(day: Date): void {
    onFocusedDateChange(day);
    const key = dateKey(day);
    const dayEvents = eventsOnDay(events, day);

    if (dayEvents.length === 0) {
      setChipFocus(null);
      if (quickCreate) setQuickCreateDate(day);
      else shouldFocusCell.current = true;
      return;
    }
    if (dayEvents.length === 1) {
      setChipFocus(null);
      onEventClick?.(dayEvents[0] as CalendarEvent);
      shouldFocusCell.current = true;
      return;
    }
    shouldFocusChip.current = true;
    setChipFocus({ dateKey: key, index: 0 });
  }

  function handleDayKeyDown(event: KeyboardEvent<HTMLButtonElement>, day: Date): void {
    switch (event.key) {
      case "ArrowLeft":
        event.preventDefault();
        moveTo(addDays(day, -1));
        break;
      case "ArrowRight":
        event.preventDefault();
        moveTo(addDays(day, 1));
        break;
      case "ArrowUp":
        event.preventDefault();
        moveTo(addDays(day, -7));
        break;
      case "ArrowDown":
        event.preventDefault();
        moveTo(addDays(day, 7));
        break;
      case "Home":
        event.preventDefault();
        moveTo(startOfWeek(day));
        break;
      case "End":
        event.preventDefault();
        moveTo(addDays(startOfWeek(day), 6));
        break;
      case "PageUp":
        event.preventDefault();
        moveTo(addMonthsKeepingWeekday(day, -1));
        break;
      case "PageDown":
        event.preventDefault();
        moveTo(addMonthsKeepingWeekday(day, 1));
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        activateDay(day);
        break;
      default:
        break;
    }
  }

  function handleChipKeyDown(event: KeyboardEvent<HTMLElement>, day: Date, focusableCount: number): void {
    const key = dateKey(day);
    if (!chipFocus || chipFocus.dateKey !== key) return;

    switch (event.key) {
      case "ArrowRight":
        event.preventDefault();
        shouldFocusChip.current = true;
        setChipFocus({ dateKey: key, index: (chipFocus.index + 1) % focusableCount });
        break;
      case "ArrowLeft":
        event.preventDefault();
        shouldFocusChip.current = true;
        setChipFocus({ dateKey: key, index: (chipFocus.index - 1 + focusableCount) % focusableCount });
        break;
      case "ArrowUp":
      case "Escape":
        event.preventDefault();
        event.stopPropagation();
        setChipFocus(null);
        // The day cell button already exists in the DOM (only its tabIndex
        // is changing, not the rendered grid), so this can focus it
        // synchronously rather than deferring to the focusedDate effect.
        cellRefs.current.get(key)?.focus();
        break;
      default:
        break;
    }
  }

  return (
    // A real <table>: biome's a11y linter refuses role="grid" on it (a
    // non-interactive element with an interactive role), so <td> buttons
    // stay individually-focusable and arrow-key-navigable without that role.
    <table aria-label="Month" className="w-full table-fixed border-collapse">
      <colgroup>
        {WEEKDAY_LABELS.map((label) => (
          <col key={label} />
        ))}
      </colgroup>
      <thead>
        <tr>
          {WEEKDAY_LABELS.map((label) => (
            <th
              key={label}
              scope="col"
              className="border-border border-b p-2 text-center font-normal text-text-secondary text-xs"
            >
              {label}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {Array.from({ length: 6 }, (_, week) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: weeks never reorder within a render.
          <tr key={week}>
            {days.slice(week * 7, week * 7 + 7).map((day) => {
              const key = dateKey(day);
              const dayEvents = eventsOnDay(events, day);
              const visibleEvents = dayEvents.slice(0, MAX_STACKED_CHIPS);
              const overflowCount = dayEvents.length - visibleEvents.length;
              const focusableChipCount = visibleEvents.length + (overflowCount > 0 ? 1 : 0);
              const isFocusedCell = chipFocus === null && isSameDay(day, focusedDate);
              const inMonth = isInMonth(day, monthStart);
              const isToday = isSameDay(day, today);

              return (
                <td key={key} className="relative min-h-24 border-border border-b p-1 align-top">
                  <button
                    type="button"
                    ref={(el) => {
                      if (el) cellRefs.current.set(key, el);
                      else cellRefs.current.delete(key);
                    }}
                    tabIndex={isFocusedCell ? 0 : -1}
                    aria-label={FULL_DATE_FORMAT.format(day)}
                    onClick={() => activateDay(day)}
                    onKeyDown={(event) => handleDayKeyDown(event, day)}
                    className="flex w-full flex-col items-center gap-1 focus-visible:shadow-focus"
                  >
                    <span
                      className={`inline-flex h-5 w-5 items-center justify-center rounded-full text-xs ${
                        isToday
                          ? "bg-primary text-text-inverse"
                          : inMonth
                            ? "text-text"
                            : "text-text-secondary opacity-60"
                      }`}
                    >
                      {day.getDate()}
                    </span>
                  </button>
                  <div className={`flex flex-col gap-0.5 ${inMonth ? "" : "opacity-60"}`}>
                    {visibleEvents.map((event, index) => (
                      <EventChip
                        key={event.id}
                        event={event}
                        onClick={onEventClick}
                        tabIndex={chipFocus?.dateKey === key && chipFocus.index === index ? 0 : -1}
                        ref={(el) => {
                          const refKey = `${key}:${index}`;
                          if (el) chipRefs.current.set(refKey, el);
                          else chipRefs.current.delete(refKey);
                        }}
                        onKeyDown={(event) => handleChipKeyDown(event, day, focusableChipCount)}
                      />
                    ))}
                    {overflowCount > 0 && (
                      <button
                        type="button"
                        ref={(el) => {
                          const refKey = `${key}:${visibleEvents.length}`;
                          if (el) chipRefs.current.set(refKey, el);
                          else chipRefs.current.delete(refKey);
                        }}
                        tabIndex={chipFocus?.dateKey === key && chipFocus.index === visibleEvents.length ? 0 : -1}
                        onClick={() => setOverflowDay(day)}
                        onKeyDown={(event) => handleChipKeyDown(event, day, focusableChipCount)}
                        className="px-1.5 text-left text-text-secondary text-xs hover:text-text"
                      >
                        +{overflowCount} more
                      </button>
                    )}
                  </div>
                  {quickCreateDate && isSameDay(quickCreateDate, day) && (
                    <QuickCreatePopover
                      date={quickCreateDate}
                      onCreate={() => {
                        onQuickCreate?.(quickCreateDate);
                        setQuickCreateDate(null);
                      }}
                      onClose={() => setQuickCreateDate(null)}
                      triggerRef={{ current: cellRefs.current.get(key) ?? null }}
                    />
                  )}
                  {overflowDay && isSameDay(overflowDay, day) && (
                    <div
                      role="dialog"
                      aria-label={`All events on ${LONG_DATE_FORMAT.format(day)}`}
                      onKeyDown={(event) => {
                        if (event.key !== "Escape") return;
                        event.preventDefault();
                        event.stopPropagation();
                        setOverflowDay(null);
                        cellRefs.current.get(key)?.focus();
                      }}
                      className={`${FLOATING_PANEL_CLASSES} min-w-48 p-2`}
                    >
                      <div className="flex flex-col gap-1">
                        {dayEvents.map((event) => (
                          <EventChip key={event.id} event={event} onClick={onEventClick} />
                        ))}
                      </div>
                      <button
                        type="button"
                        // Focuses on mount, matching QuickCreatePopover's
                        // own convention of moving focus into the popover
                        // when it opens rather than leaving it on the
                        // now-covered trigger button.
                        ref={(el) => el?.focus()}
                        onClick={() => setOverflowDay(null)}
                        className="mt-2 text-text-secondary text-xs hover:text-text"
                      >
                        Close
                      </button>
                    </div>
                  )}
                </td>
              );
            })}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
