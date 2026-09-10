// Presentational props for CalendarView and its sub-components — already-
// resolved events, not view-system.md §7's manifest fields (date_field/
// title_field/color_field/color_map), which is goerp#645's concern.

export type CalendarViewMode = "month" | "week" | "day" | "agenda";

export interface CalendarEvent {
  id: string;
  title: string;
  start: Date;
  // null matches the documented end_date_field: null example — a
  // point-in-time event rather than a timed span.
  end: Date | null;
  // Resolved color_map[color_field value] hex, e.g. "#3B82F6". Undefined
  // renders with the neutral default chip treatment.
  color?: string | undefined;
}

export interface CalendarViewProps {
  events: CalendarEvent[];
  allowedViews?: CalendarViewMode[] | undefined;
  defaultView?: CalendarViewMode | undefined;
  quickCreate?: boolean | undefined;
  // "Plain navigation, not a modal" — the caller owns actually navigating
  // to on_click's target view.
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
  onQuickCreate?: ((date: Date) => void) | undefined;
  // Fixes "today" and the initially-visible range for deterministic
  // stories/tests; defaults to the real current date.
  initialDate?: Date | undefined;
}
