import { describe, expect, it } from "vitest";
import { addDays, dueGroup, dueLabel, groupByDue, todayIn } from "./activity-dates.js";

describe("todayIn", () => {
  it("reads the calendar date in the given timezone", () => {
    const lateEvening = new Date("2026-09-25T23:30:00Z");
    expect(todayIn("Africa/Accra", lateEvening)).toBe("2026-09-25");
    expect(todayIn("Asia/Tokyo", lateEvening)).toBe("2026-09-26");
    expect(todayIn("America/Los_Angeles", new Date("2026-09-26T03:00:00Z"))).toBe("2026-09-25");
  });

  it("falls back to UTC for an unrecognized timezone", () => {
    expect(todayIn("Not/AZone", new Date("2026-09-25T23:30:00Z"))).toBe("2026-09-25");
  });
});

describe("addDays", () => {
  it("crosses month and year boundaries", () => {
    expect(addDays("2026-09-30", 1)).toBe("2026-10-01");
    expect(addDays("2026-12-31", 1)).toBe("2027-01-01");
    expect(addDays("2026-03-01", -1)).toBe("2026-02-28");
  });
});

describe("dueGroup and groupByDue", () => {
  it("splits activities into overdue, today and upcoming, keeping their order and dropping empty groups", () => {
    const today = "2026-09-25";
    expect(dueGroup("2026-09-24", today)).toBe("overdue");
    expect(dueGroup(today, today)).toBe("today");
    expect(dueGroup("2026-09-26", today)).toBe("upcoming");

    const items = [
      { id: "a", dueDate: "2026-09-20" },
      { id: "b", dueDate: "2026-09-24" },
      { id: "c", dueDate: "2026-09-27" },
    ];
    expect(groupByDue(items, today)).toEqual([
      { group: "overdue", items: [items[0], items[1]] },
      { group: "upcoming", items: [items[2]] },
    ]);
  });
});

describe("dueLabel", () => {
  it("says Today and Tomorrow, otherwise the date", () => {
    expect(dueLabel("2026-09-25", "2026-09-25")).toBe("Today");
    expect(dueLabel("2026-09-26", "2026-09-25")).toBe("Tomorrow");
    expect(dueLabel("2026-09-30", "2026-09-25", "en-US")).toBe("Sep 30, 2026");
    expect(dueLabel("2026-09-20", "2026-09-25", "en-US")).toBe("Sep 20, 2026");
  });
});
