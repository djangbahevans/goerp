import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { addDays } from "./calendar-date-utils.js";
import { CalendarTimeGrid } from "./calendar-time-grid.js";
import type { CalendarEvent } from "./calendar-view-types.js";

afterEach(cleanup);

const DAY = new Date(2026, 4, 13, 0, 0);

function timedEvent(overrides: Partial<CalendarEvent>): CalendarEvent {
  return {
    id: "1",
    title: "Standup",
    start: new Date(2026, 4, 13, 9, 0),
    end: new Date(2026, 4, 13, 9, 30),
    ...overrides,
  };
}

describe("CalendarTimeGrid", () => {
  it("renders one slot button per hour for a single day", () => {
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[]} />);
    expect(screen.getAllByRole("button")).toHaveLength(24);
  });

  it("renders a timed event's chip with its title", () => {
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[timedEvent({})]} />);
    expect(screen.getByText("Standup")).toBeTruthy();
  });

  it("renders a point-in-time event (no end) as a fixed-height chip", () => {
    const event = timedEvent({ end: null, title: "Reminder" });
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[event]} />);
    expect(screen.getByText("Reminder")).toBeTruthy();
  });

  it("renders an all-day/multi-day event in the dedicated all-day row, not a timed slot", () => {
    const event = timedEvent({
      title: "Conference",
      start: new Date(2026, 4, 13, 0, 0),
      end: new Date(2026, 4, 15, 0, 0),
    });
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[event]} />);
    expect(screen.getByText("All day")).toBeTruthy();
    expect(screen.getByText("Conference")).toBeTruthy();
  });

  it("lays out two overlapping timed events side by side, not stacked in the same lane", () => {
    const a = timedEvent({
      id: "a",
      title: "First",
      start: new Date(2026, 4, 13, 9, 0),
      end: new Date(2026, 4, 13, 10, 0),
    });
    const b = timedEvent({
      id: "b",
      title: "Second",
      start: new Date(2026, 4, 13, 9, 30),
      end: new Date(2026, 4, 13, 10, 30),
    });
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[a, b]} />);
    const firstWidth = (screen.getByText("First").closest("div[style]") as HTMLElement).style.width;
    const secondWidth = (screen.getByText("Second").closest("div[style]") as HTMLElement).style.width;
    expect(firstWidth).toBe("50%");
    expect(secondWidth).toBe("50%");
  });

  it("does not split lanes for two non-overlapping events on the same day", () => {
    const a = timedEvent({
      id: "a",
      title: "Morning",
      start: new Date(2026, 4, 13, 9, 0),
      end: new Date(2026, 4, 13, 10, 0),
    });
    const b = timedEvent({
      id: "b",
      title: "Afternoon",
      start: new Date(2026, 4, 13, 14, 0),
      end: new Date(2026, 4, 13, 15, 0),
    });
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[a, b]} />);
    const width = (screen.getByText("Morning").closest("div[style]") as HTMLElement).style.width;
    expect(width).toBe("100%");
  });

  it("gives every event in a transitively-overlapping chain the same lane width, even ones that don't directly overlap each other", () => {
    // e1 (0:00-2:00) overlaps e3 (1:00-5:00) overlaps e2 (3:00-5:00) overlaps
    // e0 (4:00-7:00) — one connected cluster of 3 concurrent lanes at its
    // busiest point, even though e1 and e0 never overlap each other.
    const e1 = timedEvent({ id: "e1", title: "E1", start: new Date(2026, 4, 13, 0), end: new Date(2026, 4, 13, 2) });
    const e3 = timedEvent({ id: "e3", title: "E3", start: new Date(2026, 4, 13, 1), end: new Date(2026, 4, 13, 5) });
    const e2 = timedEvent({ id: "e2", title: "E2", start: new Date(2026, 4, 13, 3), end: new Date(2026, 4, 13, 5) });
    const e0 = timedEvent({ id: "e0", title: "E0", start: new Date(2026, 4, 13, 4), end: new Date(2026, 4, 13, 7) });
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[e1, e3, e2, e0]} />);

    const widthOf = (title: string) => (screen.getByText(title).closest("div[style]") as HTMLElement).style.width;
    expect(widthOf("E1")).toBe(widthOf("E3"));
    expect(widthOf("E3")).toBe(widthOf("E0"));
    expect(widthOf("E1")).toBe(`${100 / 3}%`);
  });

  it("moves the focused slot down by one hour on ArrowDown", () => {
    const onFocusedDateChange = vi.fn();
    render(
      <CalendarTimeGrid
        days={[DAY]}
        focusedDate={new Date(2026, 4, 13, 9)}
        onFocusedDateChange={onFocusedDateChange}
        events={[]}
      />,
    );
    const slot = screen.getByLabelText(/9\s*AM/i);
    fireEvent.keyDown(slot, { key: "ArrowDown" });
    expect(onFocusedDateChange).toHaveBeenCalled();
  });

  it("keeps DOM focus on the equivalent hour slot when ArrowRight crosses into the next day (single-day view)", () => {
    // A day-view-shaped controlled wrapper: `days` always has length 1, so
    // dayIndex is always 0 — every ArrowLeft/Right press takes the
    // "crossing a day boundary" branch, not just at an edge.
    function Wrapper() {
      const [focusedDate, setFocusedDate] = useState(new Date(2026, 4, 13, 9));
      return (
        <CalendarTimeGrid
          days={[focusedDate]}
          focusedDate={focusedDate}
          onFocusedDateChange={setFocusedDate}
          events={[]}
        />
      );
    }
    render(<Wrapper />);
    const slot = screen.getByLabelText(/Wednesday, May 13, 2026, 9\s*AM/i);
    fireEvent.keyDown(slot, { key: "ArrowRight" });
    const nextDaySlot = screen.getByLabelText(/Thursday, May 14, 2026, 9\s*AM/i);
    expect(document.activeElement).toBe(nextDaySlot);
  });

  it("keeps DOM focus on the equivalent hour slot when ArrowLeft crosses a week boundary", () => {
    function Wrapper() {
      const [focusedDate, setFocusedDate] = useState(new Date(2026, 4, 10, 9)); // Sunday, first column
      const days = [focusedDate, addDays(focusedDate, 1)];
      return (
        <CalendarTimeGrid days={days} focusedDate={focusedDate} onFocusedDateChange={setFocusedDate} events={[]} />
      );
    }
    render(<Wrapper />);
    const slot = screen.getByLabelText(/Sunday, May 10, 2026, 9\s*AM/i);
    fireEvent.keyDown(slot, { key: "ArrowLeft" });
    const prevDaySlot = screen.getByLabelText(/Saturday, May 9, 2026, 9\s*AM/i);
    expect(document.activeElement).toBe(prevDaySlot);
  });

  it("clicking an empty slot with quickCreate opens the quick-create popover", () => {
    render(<CalendarTimeGrid days={[DAY]} focusedDate={DAY} onFocusedDateChange={vi.fn()} events={[]} quickCreate />);
    fireEvent.click(screen.getByLabelText(/9\s*AM/i));
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("clicking a slot with an event navigates to it instead of opening quick create", () => {
    const onEventClick = vi.fn();
    const event = timedEvent({});
    render(
      <CalendarTimeGrid
        days={[DAY]}
        focusedDate={DAY}
        onFocusedDateChange={vi.fn()}
        events={[event]}
        quickCreate
        onEventClick={onEventClick}
      />,
    );
    fireEvent.click(screen.getByLabelText(/9\s*AM/i));
    expect(onEventClick).toHaveBeenCalledWith(event);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
