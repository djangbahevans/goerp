// Plain Date/Intl math, no date library — matches calendar-date-utils.ts's
// own convention. All calculations are local wall-clock time (not UTC).

import { addDays, addMonths, dateKey, startOfDay, startOfMonth, startOfWeek } from "../calendar/calendar-date-utils.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import type { TimelineRangeMode } from "./timeline-view-types.js";

export { addDays, dateKey, startOfDay, startOfWeek };

export function startOfQuarter(date: Date): Date {
  const quarterStartMonth = Math.floor(date.getMonth() / 3) * 3;
  return new Date(date.getFullYear(), quarterStartMonth, 1);
}

export function startOfYear(date: Date): Date {
  return new Date(date.getFullYear(), 0, 1);
}

export function addQuarters(date: Date, quarters: number): Date {
  return addMonths(date, quarters * 3);
}

export function addYears(date: Date, years: number): Date {
  return addMonths(date, years * 12);
}

function lastDayOf(rangeStart: Date, monthsSpanned: number): Date {
  return addDays(addMonths(rangeStart, monthsSpanned), -1);
}

// The date range a mode actually renders, given its focused date — shared
// by TimelineChart (reporting it via onRangeChange) and the renderer
// resolving the manifest (computing the same range up front, before
// TimelineChart's own effect has a chance to report it). Mirrors
// calendar-date-utils.ts's visibleRange, but month/quarter/year render
// their real calendar days/months rather than a padded 42-day grid — a
// Gantt chart has no fixed day-cell grid to pad out.
export function visibleRange(focusedDate: Date, mode: TimelineRangeMode): { start: Date; end: Date } {
  if (mode === "week") {
    const start = startOfWeek(focusedDate);
    return { start, end: addDays(start, 6) };
  }
  if (mode === "month") {
    const start = startOfMonth(focusedDate);
    return { start, end: lastDayOf(start, 1) };
  }
  if (mode === "quarter") {
    const start = startOfQuarter(focusedDate);
    return { start, end: lastDayOf(start, 3) };
  }
  const start = startOfYear(focusedDate);
  return { start, end: lastDayOf(start, 12) };
}

// Advances the focused date by one unit of `mode` — TimelineChart recomputes
// visibleRange from the result.
export function navigateRange(focusedDate: Date, mode: TimelineRangeMode, direction: -1 | 1): Date {
  if (mode === "week") return addDays(focusedDate, 7 * direction);
  if (mode === "month") return addMonths(focusedDate, direction);
  if (mode === "quarter") return addQuarters(focusedDate, direction);
  return addYears(focusedDate, direction);
}

// The view mode TimelineChart itself would default to on first render.
// Unlike Calendar's allowed_views, Timeline's manifest schema declares no
// restriction field — all four ranges are always available, so this never
// needs to fall back past the declared default_range itself.
export function initialRangeMode(view: Pick<TimelineViewDeclaration, "default_range">): TimelineRangeMode {
  return view.default_range ?? "month";
}

function daysBetween(start: Date, end: Date): number {
  return Math.round((startOfDay(end).getTime() - startOfDay(start).getTime()) / 86_400_000);
}

// Header gridlines: day lines for week/month (dense enough to read
// individual dates), week-start lines for quarter, month-start lines for
// year — timeline-chart.md's stated gridline-density rule.
export function gridlineDates(range: { start: Date; end: Date }, mode: TimelineRangeMode): Date[] {
  if (mode === "week" || mode === "month") {
    const count = daysBetween(range.start, range.end) + 1;
    return Array.from({ length: count }, (_, i) => addDays(range.start, i));
  }
  if (mode === "quarter") {
    const lines: Date[] = [];
    let cursor = startOfWeek(range.start);
    if (cursor < range.start) cursor = addDays(cursor, 7);
    while (cursor <= range.end) {
      lines.push(cursor);
      cursor = addDays(cursor, 7);
    }
    return lines;
  }
  const lines: Date[] = [];
  let cursor = startOfMonth(range.start);
  while (cursor <= range.end) {
    lines.push(cursor);
    cursor = addMonths(cursor, 1);
  }
  return lines;
}

export function pixelsPerDay(trackWidthPx: number, range: { start: Date; end: Date }): number {
  const totalDays = daysBetween(range.start, range.end) + 1;
  return totalDays > 0 ? trackWidthPx / totalDays : trackWidthPx;
}

// Single source of truth for the date↔pixel conversion both bar
// positioning and pointer-drag math use.
export function dateToX(date: Date, range: { start: Date; end: Date }, pxPerDay: number): number {
  return daysBetween(range.start, date) * pxPerDay;
}

export function xToDate(x: number, range: { start: Date; end: Date }, pxPerDay: number): Date {
  return addDays(range.start, Math.round(x / pxPerDay));
}

const RANGE_LABEL_FORMAT: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };

// "Sep 14" — a single resize handle's own aria-valuetext needs just the one
// date it's adjusting, not a redundant same-date-twice range.
export function formatDate(date: Date): string {
  return new Intl.DateTimeFormat(undefined, RANGE_LABEL_FORMAT).format(date);
}

// "Sep 10 – Sep 14" — feeds a TimelineBar's accessible name (label +
// range) and the live floating label shown while dragging/resizing.
export function formatDateRange(start: Date, end: Date): string {
  return `${formatDate(start)} – ${formatDate(end)}`;
}

const GRIDLINE_FORMATS: Record<TimelineRangeMode, Intl.DateTimeFormatOptions> = {
  week: { weekday: "short", day: "numeric" },
  month: { day: "numeric" },
  quarter: { month: "short", day: "numeric" },
  year: { month: "short" },
};

// The header gridline label's own text — day-of-week+day for week, bare
// day number for month (dense, one per day), "Mon d" for quarter's
// week-start lines, bare month for year's month-start lines.
export function gridlineLabel(date: Date, mode: TimelineRangeMode): string {
  return new Intl.DateTimeFormat(undefined, GRIDLINE_FORMATS[mode]).format(date);
}
