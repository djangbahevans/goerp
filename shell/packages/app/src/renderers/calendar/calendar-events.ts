import type { Row } from "../list/list-view-types.js";
import type { CalendarViewDeclaration } from "./calendar-manifest-types.js";
import { ALL_CALENDAR_VIEWS, type CalendarEvent, type CalendarViewMode } from "./calendar-view-types.js";

// The view mode CalendarView itself would default to, given the same
// default_view/allowed_views — used to pre-compute the initial fetch range
// before CalendarView's own onVisibleRangeChange has fired.
export function initialViewMode(
  view: Pick<CalendarViewDeclaration, "default_view" | "allowed_views">,
): CalendarViewMode {
  const allowed = view.allowed_views ?? ALL_CALENDAR_VIEWS;
  if (view.default_view && allowed.includes(view.default_view)) return view.default_view;
  return allowed[0] ?? "month";
}

function toDate(value: unknown): Date | null {
  if (typeof value !== "string" && typeof value !== "number") return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

// A row missing its own date_field has nothing to place on the calendar —
// dropped rather than rendered at an arbitrary fallback date.
export function buildCalendarEvents(rows: Row[], view: CalendarViewDeclaration): CalendarEvent[] {
  const events: CalendarEvent[] = [];
  for (const row of rows) {
    const start = toDate(row[view.date_field]);
    if (!start) continue;
    const id = String(row.id ?? "");
    const title = String(row[view.title_field] ?? "");
    const end = view.end_date_field ? toDate(row[view.end_date_field]) : null;
    const colorKey = view.color_field ? row[view.color_field] : undefined;
    const color = view.color_map && typeof colorKey === "string" ? view.color_map[colorKey] : undefined;
    events.push({ id, title, start, end, ...(color ? { color } : {}) });
  }
  return events;
}
