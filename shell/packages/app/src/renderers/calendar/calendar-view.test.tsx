import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CalendarView } from "./calendar-view.js";
import type { CalendarEvent } from "./calendar-view-types.js";

afterEach(cleanup);

const INITIAL_DATE = new Date(2026, 4, 13);

function makeEvent(overrides: Partial<CalendarEvent>): CalendarEvent {
  return { id: "1", title: "Event", start: INITIAL_DATE, end: null, ...overrides };
}

describe("CalendarView", () => {
  it("defaults to the month view when no defaultView is given", () => {
    render(<CalendarView events={[]} initialDate={INITIAL_DATE} />);
    expect(screen.getByRole("table", { name: "Month" })).toBeTruthy();
  });

  it("honors defaultView when it's one of allowedViews", () => {
    render(<CalendarView events={[makeEvent({})]} initialDate={INITIAL_DATE} defaultView="agenda" />);
    expect(screen.getByText("Event")).toBeTruthy();
  });

  it("falls back to the first allowed view when defaultView isn't in allowedViews", () => {
    render(<CalendarView events={[]} initialDate={INITIAL_DATE} defaultView="day" allowedViews={["week", "agenda"]} />);
    expect(screen.getByRole("table", { name: "Schedule" })).toBeTruthy();
  });

  it("only renders tabs for the allowed views", () => {
    render(<CalendarView events={[]} initialDate={INITIAL_DATE} allowedViews={["month", "agenda"]} />);
    expect(screen.getByRole("tab", { name: "Month" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Agenda" })).toBeTruthy();
    expect(screen.queryByRole("tab", { name: "Week" })).toBeNull();
    expect(screen.queryByRole("tab", { name: "Day" })).toBeNull();
  });

  it("switches views when a tab is clicked, preserving the focused/anchor date", () => {
    const event = makeEvent({ title: "Agenda event" });
    render(<CalendarView events={[event]} initialDate={INITIAL_DATE} />);
    fireEvent.click(screen.getByRole("tab", { name: "Agenda" }));
    expect(screen.getByText("Agenda event")).toBeTruthy();
  });

  it("calls onEventClick when an event is activated from the month view", () => {
    const onEventClick = vi.fn();
    const event = makeEvent({});
    render(<CalendarView events={[event]} initialDate={INITIAL_DATE} onEventClick={onEventClick} />);
    fireEvent.click(screen.getByText("Event"));
    expect(onEventClick).toHaveBeenCalledWith(event);
  });
});
