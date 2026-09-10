import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CalendarMonthGrid } from "./calendar-month-grid.js";
import type { CalendarEvent } from "./calendar-view-types.js";

afterEach(cleanup);

const TODAY = new Date(2026, 4, 13); // Wednesday, May 13 2026

function makeEvent(overrides: Partial<CalendarEvent>): CalendarEvent {
  return { id: "1", title: "Event", start: TODAY, end: null, ...overrides };
}

describe("CalendarMonthGrid", () => {
  it("renders a 6-week grid of day cells", () => {
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={[]} />);
    // 42 day-cell buttons + however many event chips (none here).
    expect(screen.getAllByRole("button")).toHaveLength(42);
  });

  it("shows a ring on today's date", () => {
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={[]} />);
    const todayCell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    expect(todayCell.querySelector("span")?.className).toContain("bg-primary");
  });

  it("dims days outside the current month", () => {
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={[]} />);
    // April 26, 2026 is a leading day of May's grid.
    const leadingDay = screen.getByLabelText(
      new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(new Date(2026, 3, 26)),
    );
    expect(leadingDay.querySelector("span")?.className).toContain("text-text-secondary");
  });

  it("stacks up to 3 chips and shows a +N more button beyond that", () => {
    const events = Array.from({ length: 5 }, (_, i) => makeEvent({ id: String(i), title: `Event ${i}` }));
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    expect(screen.getAllByText(/^Event \d$/)).toHaveLength(3);
    expect(screen.getByText("+2 more")).toBeTruthy();
  });

  it("moves focus one day at a time with ArrowRight", () => {
    const onFocusedDateChange = vi.fn();
    render(
      <CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={onFocusedDateChange} today={TODAY} events={[]} />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "ArrowRight" });
    expect(onFocusedDateChange).toHaveBeenCalledWith(new Date(2026, 4, 14));
  });

  it("moves to the start of the week on Home", () => {
    const onFocusedDateChange = vi.fn();
    render(
      <CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={onFocusedDateChange} today={TODAY} events={[]} />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Home" });
    expect(onFocusedDateChange).toHaveBeenCalledWith(new Date(2026, 4, 10));
  });

  it("navigates a month back on PageUp, keeping the same weekday", () => {
    const onFocusedDateChange = vi.fn();
    render(
      <CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={onFocusedDateChange} today={TODAY} events={[]} />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "PageUp" });
    expect(onFocusedDateChange).toHaveBeenCalledWith(new Date(2026, 3, 8));
  });

  it("Enter on an empty day with quickCreate opens the quick-create popover", () => {
    render(
      <CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={[]} quickCreate />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Enter" });
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("Enter on a day with exactly one event navigates to it", () => {
    const onEventClick = vi.fn();
    const event = makeEvent({ title: "Solo event" });
    render(
      <CalendarMonthGrid
        focusedDate={TODAY}
        onFocusedDateChange={vi.fn()}
        today={TODAY}
        events={[event]}
        onEventClick={onEventClick}
      />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Enter" });
    expect(onEventClick).toHaveBeenCalledWith(event);
  });

  it("Enter on a day with multiple events moves focus into the first chip instead of navigating", () => {
    const onEventClick = vi.fn();
    const events = [makeEvent({ id: "a", title: "First" }), makeEvent({ id: "b", title: "Second" })];
    render(
      <CalendarMonthGrid
        focusedDate={TODAY}
        onFocusedDateChange={vi.fn()}
        today={TODAY}
        events={events}
        onEventClick={onEventClick}
      />,
    );
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Enter" });
    expect(onEventClick).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByText("First"));
  });

  it("ArrowRight inside a day's chip list moves between that day's chips, wrapping", () => {
    const events = [makeEvent({ id: "a", title: "First" }), makeEvent({ id: "b", title: "Second" })];
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Enter" });
    fireEvent.keyDown(screen.getByText("First"), { key: "ArrowRight" });
    expect(document.activeElement).toBe(screen.getByText("Second"));
    fireEvent.keyDown(screen.getByText("Second"), { key: "ArrowRight" });
    expect(document.activeElement).toBe(screen.getByText("First"));
  });

  it("Escape from a chip returns focus to the day cell", () => {
    const events = [makeEvent({ id: "a", title: "First" }), makeEvent({ id: "b", title: "Second" })];
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.keyDown(cell, { key: "Enter" });
    fireEvent.keyDown(screen.getByText("First"), { key: "Escape" });
    expect(document.activeElement).toBe(cell);
  });

  it("clicking the +N more button opens a popover listing every event for that day", () => {
    const events = Array.from({ length: 5 }, (_, i) => makeEvent({ id: String(i), title: `Event ${i}` }));
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    fireEvent.click(screen.getByText("+2 more"));
    expect(within(screen.getByRole("dialog")).getAllByText(/^Event \d$/)).toHaveLength(5);
  });

  it("moves focus into the +N more popover when it opens", () => {
    const events = Array.from({ length: 5 }, (_, i) => makeEvent({ id: String(i), title: `Event ${i}` }));
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    fireEvent.click(screen.getByText("+2 more"));
    expect(within(screen.getByRole("dialog")).getByText("Close")).toBe(document.activeElement);
  });

  it("Escape closes the +N more popover and returns focus to the day cell", () => {
    const events = Array.from({ length: 5 }, (_, i) => makeEvent({ id: String(i), title: `Event ${i}` }));
    render(<CalendarMonthGrid focusedDate={TODAY} onFocusedDateChange={vi.fn()} today={TODAY} events={events} />);
    const cell = screen.getByLabelText(new Intl.DateTimeFormat(undefined, { dateStyle: "full" }).format(TODAY));
    fireEvent.click(screen.getByText("+2 more"));
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(cell);
  });
});
