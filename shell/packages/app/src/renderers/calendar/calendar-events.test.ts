import { describe, expect, it } from "vitest";
import { buildCalendarEvents, initialViewMode } from "./calendar-events.js";
import type { CalendarViewDeclaration } from "./calendar-manifest-types.js";

const baseView: CalendarViewDeclaration = {
  name: "activities_calendar",
  type: "calendar",
  resource: "contacts.activity",
  label: "Activity Calendar",
  date_field: "scheduled_at",
  title_field: "subject",
};

describe("initialViewMode", () => {
  it("uses default_view when it's one of allowed_views", () => {
    expect(initialViewMode({ default_view: "week", allowed_views: ["week", "agenda"] })).toBe("week");
  });

  it("falls back to the first allowed view when default_view isn't in allowed_views", () => {
    expect(initialViewMode({ default_view: "day", allowed_views: ["week", "agenda"] })).toBe("week");
  });

  it("defaults to month when neither field is declared", () => {
    expect(initialViewMode({})).toBe("month");
  });
});

describe("buildCalendarEvents", () => {
  it("maps date_field/title_field/end_date_field into a CalendarEvent", () => {
    const events = buildCalendarEvents(
      [{ id: "e1", scheduled_at: "2026-05-13T09:00:00Z", ends_at: "2026-05-13T10:00:00Z", subject: "Call Acme" }],
      { ...baseView, end_date_field: "ends_at" },
    );
    expect(events).toEqual([
      {
        id: "e1",
        title: "Call Acme",
        start: new Date("2026-05-13T09:00:00Z"),
        end: new Date("2026-05-13T10:00:00Z"),
      },
    ]);
  });

  it("drops a row with no valid date_field value rather than placing it at a fallback date", () => {
    const events = buildCalendarEvents(
      [
        { id: "e1", scheduled_at: null, subject: "No date" },
        { id: "e2", scheduled_at: "2026-05-13T09:00:00Z", subject: "Has date" },
      ],
      baseView,
    );
    expect(events).toHaveLength(1);
    expect(events[0]?.id).toBe("e2");
  });

  it("resolves color_field through color_map", () => {
    const events = buildCalendarEvents(
      [{ id: "e1", scheduled_at: "2026-05-13T09:00:00Z", subject: "Call", type: "call" }],
      {
        ...baseView,
        color_field: "type",
        color_map: { call: "#3B82F6" },
      },
    );
    expect(events[0]?.color).toBe("#3B82F6");
  });

  it("leaves color undefined when color_field's value has no entry in color_map", () => {
    const events = buildCalendarEvents(
      [{ id: "e1", scheduled_at: "2026-05-13T09:00:00Z", subject: "Call", type: "unmapped" }],
      { ...baseView, color_field: "type", color_map: { call: "#3B82F6" } },
    );
    expect(events[0]?.color).toBeUndefined();
  });
});
