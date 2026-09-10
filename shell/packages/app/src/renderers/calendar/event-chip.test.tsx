import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CalendarEvent } from "./calendar-view-types.js";
import { EventChip } from "./event-chip.js";

afterEach(cleanup);

const event: CalendarEvent = {
  id: "1",
  title: "Team standup",
  start: new Date(2026, 4, 10, 9, 0),
  end: new Date(2026, 4, 10, 9, 30),
  color: "#3B82F6",
};

describe("EventChip", () => {
  it("renders the event title", () => {
    render(<EventChip event={event} />);
    expect(screen.getByText("Team standup")).toBeTruthy();
  });

  it("combines title and time into its accessible name, not color alone", () => {
    render(<EventChip event={event} />);
    const chip = screen.getByRole("button");
    expect(chip.getAttribute("aria-label")).toContain("Team standup");
    expect(chip.getAttribute("aria-label")).toMatch(/\d/);
  });

  it("calls onClick with the event when activated", () => {
    const onClick = vi.fn();
    render(<EventChip event={event} onClick={onClick} />);
    fireEvent.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledWith(event);
  });

  it("applies the color_map fill as an inline background color", () => {
    render(<EventChip event={event} />);
    expect(screen.getByRole("button").style.backgroundColor).toBe("rgb(59, 130, 246)");
  });

  it("falls back to a neutral chip treatment when no color is given", () => {
    const { color, ...uncolored } = event;
    render(<EventChip event={uncolored} />);
    const chip = screen.getByRole("button");
    expect(chip.style.backgroundColor).toBe("");
    expect(chip.className).toContain("bg-bg-subtle");
  });

  it("respects a custom tabIndex for the nested day-cell roving pattern", () => {
    render(<EventChip event={event} tabIndex={-1} />);
    expect(screen.getByRole("button").tabIndex).toBe(-1);
  });
});
