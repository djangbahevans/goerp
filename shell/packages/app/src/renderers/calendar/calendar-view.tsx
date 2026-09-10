import { TabPanel, Tabs } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { useState } from "react";
import { CalendarAgendaList } from "./calendar-agenda-list.js";
import { CalendarDayView } from "./calendar-day-view.js";
import { CalendarMonthGrid } from "./calendar-month-grid.js";
import type { CalendarViewMode, CalendarViewProps } from "./calendar-view-types.js";
import { CalendarWeekView } from "./calendar-week-view.js";

const ALL_VIEWS: CalendarViewMode[] = ["month", "week", "day", "agenda"];
const VIEW_LABELS: Record<CalendarViewMode, string> = { month: "Month", week: "Week", day: "Day", agenda: "Agenda" };

export function CalendarView({
  events,
  allowedViews = ALL_VIEWS,
  defaultView,
  quickCreate = false,
  onEventClick,
  onQuickCreate,
  initialDate,
}: CalendarViewProps): ReactNode {
  const [today] = useState(() => initialDate ?? new Date());
  const [focusedDate, setFocusedDate] = useState(() => initialDate ?? new Date());
  const [activeView, setActiveView] = useState<CalendarViewMode>(() =>
    defaultView && allowedViews.includes(defaultView)
      ? defaultView
      : ((allowedViews[0] ?? "month") as CalendarViewMode),
  );

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
