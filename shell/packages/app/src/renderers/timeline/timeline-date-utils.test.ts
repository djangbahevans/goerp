import { describe, expect, it } from "vitest";
import {
  dateToX,
  gridlineDates,
  gridlineLabel,
  initialRangeMode,
  navigateRange,
  pixelsPerDay,
  visibleRange,
} from "./timeline-date-utils.js";

function key(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

describe("visibleRange", () => {
  const focused = new Date(2026, 4, 13); // Wednesday, May 13 2026

  it("week mode spans the containing Sunday-to-Saturday week", () => {
    const range = visibleRange(focused, "week");
    expect(key(range.start)).toBe("2026-05-10");
    expect(key(range.end)).toBe("2026-05-16");
  });

  it("month mode spans the real calendar month, not a padded 42-day grid", () => {
    const range = visibleRange(focused, "month");
    expect(key(range.start)).toBe("2026-05-01");
    expect(key(range.end)).toBe("2026-05-31");
  });

  it("quarter mode spans the containing 3-month quarter", () => {
    const range = visibleRange(focused, "quarter");
    expect(key(range.start)).toBe("2026-04-01");
    expect(key(range.end)).toBe("2026-06-30");
  });

  it("year mode spans the full calendar year", () => {
    const range = visibleRange(focused, "year");
    expect(key(range.start)).toBe("2026-01-01");
    expect(key(range.end)).toBe("2026-12-31");
  });
});

describe("navigateRange", () => {
  it("advances by one unit for each mode", () => {
    const focused = new Date(2026, 4, 13);
    expect(key(navigateRange(focused, "week", 1))).toBe("2026-05-20");
    expect(key(navigateRange(focused, "month", 1))).toBe("2026-06-13");
    expect(key(navigateRange(focused, "quarter", 1))).toBe("2026-08-13");
    expect(key(navigateRange(focused, "year", 1))).toBe("2027-05-13");
  });

  it("retreats by one unit given -1", () => {
    const focused = new Date(2026, 4, 13);
    expect(key(navigateRange(focused, "week", -1))).toBe("2026-05-06");
  });
});

describe("gridlineDates", () => {
  it("week/month mode produces one line per day", () => {
    const range = visibleRange(new Date(2026, 4, 13), "week");
    expect(gridlineDates(range, "week")).toHaveLength(7);
  });

  it("quarter mode produces one line per week start", () => {
    const range = visibleRange(new Date(2026, 4, 13), "quarter");
    const lines = gridlineDates(range, "quarter");
    expect(lines.length).toBeGreaterThan(10);
    expect(lines.length).toBeLessThan(14);
  });

  it("year mode produces one line per month start", () => {
    const range = visibleRange(new Date(2026, 4, 13), "year");
    expect(gridlineDates(range, "year")).toHaveLength(12);
  });
});

describe("dateToX", () => {
  it("places a date at its day-offset from range.start, in pixels", () => {
    const range = { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) };
    const pxPerDay = pixelsPerDay(310, range);
    expect(dateToX(new Date(2026, 4, 15), range, pxPerDay)).toBe(14 * pxPerDay);
  });
});

describe("initialRangeMode", () => {
  it("falls back to month when default_range is absent", () => {
    expect(initialRangeMode({})).toBe("month");
  });

  it("uses the declared default_range", () => {
    expect(initialRangeMode({ default_range: "quarter" })).toBe("quarter");
  });
});

describe("gridlineLabel", () => {
  it("formats differently per mode", () => {
    const date = new Date(2026, 8, 14); // Sep 14 2026
    expect(gridlineLabel(date, "month")).toBe("14");
    expect(gridlineLabel(date, "year")).toContain("Sep");
  });
});
