import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CalendarAgendaList } from "./calendar-agenda-list.js";
import type { CalendarEvent } from "./calendar-view-types.js";

afterEach(cleanup);

const TODAY = new Date(2026, 4, 13);

function makeEvent(overrides: Partial<CalendarEvent>): CalendarEvent {
  return { id: "1", title: "Event", start: TODAY, end: null, ...overrides };
}

describe("CalendarAgendaList", () => {
  it("shows a fallback message when there are no events", () => {
    render(<CalendarAgendaList today={TODAY} events={[]} />);
    expect(screen.getByText("No events.")).toBeTruthy();
  });

  it("groups events by day with a date subheader", () => {
    const events = [
      makeEvent({ id: "a", title: "Day 1 event", start: new Date(2026, 4, 13, 9) }),
      makeEvent({ id: "b", title: "Day 2 event", start: new Date(2026, 4, 14, 9) }),
    ];
    render(<CalendarAgendaList today={TODAY} events={events} />);
    expect(screen.getAllByRole("heading", { level: 3 })).toHaveLength(2);
    expect(screen.getByText("Day 1 event")).toBeTruthy();
    expect(screen.getByText("Day 2 event")).toBeTruthy();
  });

  it("lists events chronologically within a day", () => {
    const events = [
      makeEvent({ id: "a", title: "Later", start: new Date(2026, 4, 13, 14) }),
      makeEvent({ id: "b", title: "Earlier", start: new Date(2026, 4, 13, 9) }),
    ];
    render(<CalendarAgendaList today={TODAY} events={events} />);
    const rows = screen.getAllByRole("listitem");
    expect(rows[0]?.textContent).toContain("Earlier");
    expect(rows[1]?.textContent).toContain("Later");
  });

  it("calls onEventClick when a row is activated", () => {
    const onEventClick = vi.fn();
    const event = makeEvent({});
    render(<CalendarAgendaList today={TODAY} events={[event]} onEventClick={onEventClick} />);
    fireEvent.click(screen.getByText("Event"));
    expect(onEventClick).toHaveBeenCalledWith(event);
  });
});
