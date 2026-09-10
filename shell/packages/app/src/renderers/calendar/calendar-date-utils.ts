// Plain Date/Intl math, no date library — matches date-fields.tsx's own
// convention. All calculations are local wall-clock time (not UTC), since
// a calendar grid should show days as the viewer's own clock sees them.

const EVENT_TIME_FORMAT: Intl.DateTimeFormatOptions = { hour: "numeric", minute: "2-digit" };

export function formatEventTime(start: Date, end: Date | null): string {
  const startLabel = new Intl.DateTimeFormat(undefined, EVENT_TIME_FORMAT).format(start);
  if (!end) return startLabel;
  return `${startLabel}–${new Intl.DateTimeFormat(undefined, EVENT_TIME_FORMAT).format(end)}`;
}

export function startOfDay(date: Date): Date {
  const result = new Date(date);
  result.setHours(0, 0, 0, 0);
  return result;
}

export function addDays(date: Date, days: number): Date {
  const result = new Date(date);
  result.setDate(result.getDate() + days);
  return result;
}

// Clamps to the target month's last day rather than letting Date.setMonth
// overflow into the following month (e.g. Jan 31 + 1 month must land in
// February, not silently roll into March).
export function addMonths(date: Date, months: number): Date {
  const targetMonthStart = new Date(date.getFullYear(), date.getMonth() + months, 1);
  const daysInTargetMonth = new Date(targetMonthStart.getFullYear(), targetMonthStart.getMonth() + 1, 0).getDate();
  const result = new Date(targetMonthStart);
  result.setDate(Math.min(date.getDate(), daysInTargetMonth));
  result.setHours(date.getHours(), date.getMinutes(), date.getSeconds(), date.getMilliseconds());
  return result;
}

export function startOfMonth(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

// PageUp/PageDown's "keeping the same day-of-week focused where possible":
// advances by a whole month, then snaps to the day in that month sharing
// the original weekday, at the same week-of-month position (falling back
// one week if that position spills past the month's end).
export function addMonthsKeepingWeekday(date: Date, months: number): Date {
  const weekday = date.getDay();
  const weekOfMonth = Math.floor((date.getDate() - 1) / 7);
  const newMonthStart = startOfMonth(addMonths(date, months));
  const offsetToWeekday = (weekday - newMonthStart.getDay() + 7) % 7;
  const candidate = addDays(newMonthStart, offsetToWeekday + weekOfMonth * 7);
  return isInMonth(candidate, newMonthStart) || weekOfMonth === 0 ? candidate : addDays(candidate, -7);
}

export function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function eventsOnDay<T extends { start: Date }>(events: T[], day: Date): T[] {
  return events.filter((event) => isSameDay(event.start, day));
}

export function dateKey(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

// Sunday-first week, matching Intl's default (undefined locale) week start
// used elsewhere in this codebase (date-fields.tsx's Intl.DateTimeFormat).
export function startOfWeek(date: Date): Date {
  return addDays(startOfDay(date), -date.getDay());
}

// A 6-week (42-day) month grid, always starting on the Sunday on/before the
// 1st and ending on the Saturday on/after the last day — the standard
// month-calendar shape, including leading/trailing days from adjacent
// months (view-system.md: "still real, clickable days").
export function monthGridDays(monthStart: Date): Date[] {
  const gridStart = startOfWeek(monthStart);
  return Array.from({ length: 42 }, (_, i) => addDays(gridStart, i));
}

export function weekDays(weekStart: Date): Date[] {
  return Array.from({ length: 7 }, (_, i) => addDays(weekStart, i));
}

export function isInMonth(date: Date, monthStart: Date): boolean {
  return date.getFullYear() === monthStart.getFullYear() && date.getMonth() === monthStart.getMonth();
}
