import type { ReactNode } from "react";
import { CalendarTimeGrid } from "./calendar-time-grid.js";
import type { CalendarEvent } from "./calendar-view-types.js";

export interface CalendarDayViewProps {
  focusedDate: Date;
  onFocusedDateChange: (date: Date) => void;
  events: CalendarEvent[];
  quickCreate?: boolean | undefined;
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
  onQuickCreate?: ((date: Date) => void) | undefined;
}

export function CalendarDayView({
  focusedDate,
  onFocusedDateChange,
  events,
  ...rest
}: CalendarDayViewProps): ReactNode {
  return (
    <CalendarTimeGrid
      days={[focusedDate]}
      focusedDate={focusedDate}
      onFocusedDateChange={onFocusedDateChange}
      events={events}
      {...rest}
    />
  );
}
