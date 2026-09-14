import { TabPanel, Tabs } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import { CalendarAgendaList } from "./calendar-agenda-list.js";
import { visibleRange } from "./calendar-date-utils.js";
import { CalendarDayView } from "./calendar-day-view.js";
import { CalendarMonthGrid } from "./calendar-month-grid.js";
import { ALL_CALENDAR_VIEWS, type CalendarViewMode, type CalendarViewProps } from "./calendar-view-types.js";
import { CalendarWeekView } from "./calendar-week-view.js";

const VIEW_LABELS: Record<CalendarViewMode, string> = { month: "Month", week: "Week", day: "Day", agenda: "Agenda" };

export function CalendarView({
  events,
  allowedViews = ALL_CALENDAR_VIEWS,
  defaultView,
  quickCreate = false,
  onEventClick,
  onQuickCreate,
  initialDate,
  onVisibleRangeChange,
}: CalendarViewProps): ReactNode {
  const [today] = useState(() => initialDate ?? new Date());
  const [focusedDate, setFocusedDate] = useState(() => initialDate ?? new Date());
  const [activeView, setActiveView] = useState<CalendarViewMode>(() =>
    defaultView && allowedViews.includes(defaultView)
      ? defaultView
      : ((allowedViews[0] ?? "month") as CalendarViewMode),
  );

  // onVisibleRangeChange isn't a dep: it's expected to close over a stable
  // setter, so a fresh closure each render shouldn't re-run this effect.
  // biome-ignore lint/correctness/useExhaustiveDependencies: see above.
  useEffect(() => {
    // biome-ignore lint/nursery/useReactCompiler: see above.
    onVisibleRangeChange?.(visibleRange(focusedDate, activeView));
  }, [focusedDate, activeView]);

  return (
    <div className="flex flex-col gap-3">
      <Tabs
        items={allowedViews.map((view) => ({ id: view, label: VIEW_LABELS[view] }))}
        activeId={activeView}
        onChange={(id) => setActiveView(id as CalendarViewMode)}
      >
        <TabPanel id="month">
          <CalendarMonthGrid
            focusedDate={focusedDate}
            onFocusedDateChange={setFocusedDate}
            today={today}
            events={events}
            quickCreate={quickCreate}
            onEventClick={onEventClick}
            onQuickCreate={onQuickCreate}
          />
        </TabPanel>
        <TabPanel id="week">
          <CalendarWeekView
            focusedDate={focusedDate}
            onFocusedDateChange={setFocusedDate}
            events={events}
            quickCreate={quickCreate}
            onEventClick={onEventClick}
            onQuickCreate={onQuickCreate}
          />
        </TabPanel>
        <TabPanel id="day">
          <CalendarDayView
            focusedDate={focusedDate}
            onFocusedDateChange={setFocusedDate}
            events={events}
            quickCreate={quickCreate}
            onEventClick={onEventClick}
            onQuickCreate={onQuickCreate}
          />
        </TabPanel>
        <TabPanel id="agenda">
          <CalendarAgendaList today={today} events={events} onEventClick={onEventClick} />
        </TabPanel>
      </Tabs>
    </div>
  );
}
