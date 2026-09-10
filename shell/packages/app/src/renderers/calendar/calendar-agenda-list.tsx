import type { KeyboardEvent, ReactNode } from "react";
import { dateKey, formatEventTime, isSameDay } from "./calendar-date-utils.js";
import type { CalendarEvent } from "./calendar-view-types.js";

export interface CalendarAgendaListProps {
  today: Date;
  events: CalendarEvent[];
  onEventClick?: ((event: CalendarEvent) => void) | undefined;
}

interface DayGroup {
  key: string;
  day: Date;
  events: CalendarEvent[];
}

function groupByDay(events: CalendarEvent[]): DayGroup[] {
  const groups = new Map<string, DayGroup>();
  for (const event of [...events].sort((a, b) => a.start.getTime() - b.start.getTime())) {
    const key = dateKey(event.start);
    const group = groups.get(key);
    if (group) group.events.push(event);
    else groups.set(key, { key, day: event.start, events: [event] });
  }
  return [...groups.values()];
}

// DataTable's own row treatment (data-table.tsx): border-border border-b
// divider, p-3 padding, bg-surface, hover:bg-surface-hover, text-base
// text-text body copy, and its clickable-row focus ring — reused directly
// per the design doc, without DataTable's column machinery.
const ROW_CLASSES =
  "flex w-full cursor-pointer items-center gap-3 border-border border-b bg-surface p-3 text-left text-base text-text hover:bg-surface-hover focus-visible:[outline:2px_solid_var(--color-primary)] focus-visible:-outline-offset-2";

export function CalendarAgendaList({ today, events, onEventClick }: CalendarAgendaListProps): ReactNode {
  const groups = groupByDay(events);

  function handleKeyDown(event: KeyboardEvent<HTMLButtonElement>, calendarEvent: CalendarEvent): void {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    onEventClick?.(calendarEvent);
  }

  if (groups.length === 0) {
    return (
      <p role="status" aria-label="Agenda" className="p-3 text-sm text-text-secondary">
        No events.
      </p>
    );
  }

  return (
    <div>
      {groups.map((group) => {
        const dateLabel = new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(group.day);
        return (
          <section key={group.key} aria-label={dateLabel}>
            <h3 className="border-border border-b bg-bg-subtle px-3 py-1.5 font-medium text-xs">
              <span className={isSameDay(group.day, today) ? "text-primary" : "text-text-secondary"}>{dateLabel}</span>
            </h3>
            <ol>
              {group.events.map((event) => (
                <li key={event.id}>
                  <button
                    type="button"
                    onClick={() => onEventClick?.(event)}
                    onKeyDown={(keyboardEvent) => handleKeyDown(keyboardEvent, event)}
                    className={ROW_CLASSES}
                  >
                    {event.color && (
                      <span
                        aria-hidden
                        className="h-2.5 w-2.5 shrink-0 rounded-full"
                        style={{ backgroundColor: event.color }}
                      />
                    )}
                    <span className="w-24 shrink-0 text-text-secondary text-sm">
                      {formatEventTime(event.start, event.end)}
                    </span>
                    <span className="truncate">{event.title}</span>
                  </button>
                </li>
              ))}
            </ol>
          </section>
        );
      })}
    </div>
  );
}
