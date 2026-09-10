import type { ReactNode } from "react";
import { startOfWeek, weekDays } from "./calendar-date-utils.js";
import { CalendarTimeGrid } from "./calendar-time-grid.js";
import type { CalendarEvent } from "./calendar-view-types.js";

export interface CalendarWeekViewProps {
  focusedDate: Date;
  onFocusedDateChange: (date: Date) => void;
  events: CalendarEvent[];
  quickCreate?: boolean | undefined;
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
  onQuickCreate?: ((date: Date) => void) | undefined;
}

export function CalendarWeekView({
  focusedDate,
  onFocusedDateChange,
  events,
  ...rest
}: CalendarWeekViewProps): ReactNode {
  return (
    <CalendarTimeGrid
      days={weekDays(startOfWeek(focusedDate))}
      focusedDate={focusedDate}
      onFocusedDateChange={onFocusedDateChange}
      events={events}
      {...rest}
    />
  );
}
