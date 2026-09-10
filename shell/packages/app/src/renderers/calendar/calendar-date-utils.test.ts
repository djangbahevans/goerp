import { describe, expect, it } from "vitest";
import {
  addDays,
  addMonths,
  addMonthsKeepingWeekday,
  dateKey,
  isInMonth,
  isSameDay,
  monthGridDays,
  startOfWeek,
  weekDays,
} from "./calendar-date-utils.js";

describe("calendar-date-utils", () => {
  it("addDays advances the calendar date, crossing month boundaries", () => {
    expect(dateKey(addDays(new Date(2026, 0, 30), 3))).toBe("2026-02-02");
  });

  it("addMonths advances the month, keeping the day-of-month where possible", () => {
    expect(dateKey(addMonths(new Date(2026, 0, 15), 1))).toBe("2026-02-15");
  });

  it("addMonths clamps into the target month rather than overflowing past it", () => {
    // 2026 is not a leap year, so February has 28 days — a naive
    // Date.setMonth call rolls Jan 29/30/31 into March instead of clamping.
    expect(dateKey(addMonths(new Date(2026, 0, 29), 1))).toBe("2026-02-28");
    expect(dateKey(addMonths(new Date(2026, 0, 30), 1))).toBe("2026-02-28");
    expect(dateKey(addMonths(new Date(2026, 0, 31), 1))).toBe("2026-02-28");
  });

  it("addMonths clamps going backward too (31-day month into a 30-day one)", () => {
    expect(dateKey(addMonths(new Date(2026, 4, 31), -1))).toBe("2026-04-30");
  });

  it("isSameDay compares calendar date, ignoring time-of-day", () => {
    expect(isSameDay(new Date(2026, 4, 10, 9, 0), new Date(2026, 4, 10, 23, 59))).toBe(true);
    expect(isSameDay(new Date(2026, 4, 10), new Date(2026, 4, 11))).toBe(false);
  });

  it("startOfWeek rewinds to the preceding Sunday", () => {
    // 2026-05-13 is a Wednesday.
    expect(dateKey(startOfWeek(new Date(2026, 4, 13)))).toBe("2026-05-10");
  });

  it("startOfWeek is a no-op on a Sunday", () => {
    expect(dateKey(startOfWeek(new Date(2026, 4, 10)))).toBe("2026-05-10");
  });

  it("monthGridDays produces a 42-day grid starting on the Sunday on/before the 1st", () => {
    const days = monthGridDays(new Date(2026, 4, 1)); // May 2026, 1st is a Friday
    expect(days).toHaveLength(42);
    expect(dateKey(days[0] as Date)).toBe("2026-04-26");
    expect(dateKey(days[41] as Date)).toBe("2026-06-06");
  });

  it("weekDays produces 7 consecutive days from the given start", () => {
    const days = weekDays(new Date(2026, 4, 10));
    expect(days).toHaveLength(7);
    expect(dateKey(days[6] as Date)).toBe("2026-05-16");
  });

  it("isInMonth checks calendar year+month, not the exact day", () => {
    expect(isInMonth(new Date(2026, 4, 1), new Date(2026, 4, 15))).toBe(true);
    expect(isInMonth(new Date(2026, 3, 30), new Date(2026, 4, 15))).toBe(false);
  });

  describe("addMonthsKeepingWeekday", () => {
    it("advances a month, landing on the same weekday at the same week-of-month position", () => {
      // 2026-05-13 is a Wednesday, the 2nd Wednesday of May.
      expect(dateKey(addMonthsKeepingWeekday(new Date(2026, 4, 13), 1))).toBe("2026-06-10");
    });

    it("goes back a month the same way", () => {
      expect(dateKey(addMonthsKeepingWeekday(new Date(2026, 4, 13), -1))).toBe("2026-04-08");
    });

    it("lands in the very next month even when the focused day-of-month doesn't exist there", () => {
      // 2026-01-29: a naive Date.setMonth(1) call overflows past February
      // (28 days in 2026) into March, skipping a whole month of PageDown.
      const result = addMonthsKeepingWeekday(new Date(2026, 0, 29), 1);
      expect(result.getMonth()).toBe(1); // February, not March
    });

    it("falls back a week when the same week-of-month position would spill into the next month", () => {
      // 2026-03-29 is a Sunday, the 5th Sunday of March; April has only 4 full Sundays before spilling into May.
      expect(dateKey(addMonthsKeepingWeekday(new Date(2026, 2, 29), 1))).toBe("2026-04-26");
    });
  });
});
