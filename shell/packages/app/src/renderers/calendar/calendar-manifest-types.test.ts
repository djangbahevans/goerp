import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { CalendarViewDeclarationSchema } from "./calendar-manifest-types.js";

const base = {
  name: "activities_calendar",
  type: "calendar",
  resource: "contacts.activity",
  label: "Activity Calendar",
  date_field: "scheduled_at",
  title_field: "subject",
};

describe("CalendarViewDeclarationSchema", () => {
  it("accepts a minimal valid declaration", () => {
    expect(v.safeParse(CalendarViewDeclarationSchema, base).success).toBe(true);
  });

  it("requires date_field and title_field", () => {
    const { date_field: _dateField, ...withoutDateField } = base;
    expect(v.safeParse(CalendarViewDeclarationSchema, withoutDateField).success).toBe(false);
  });

  it("rejects a default_view/allowed_views value outside the documented enum", () => {
    expect(v.safeParse(CalendarViewDeclarationSchema, { ...base, default_view: "year" }).success).toBe(false);
    expect(v.safeParse(CalendarViewDeclarationSchema, { ...base, allowed_views: ["month", "year"] }).success).toBe(
      false,
    );
  });

  it("accepts a valid color_map and allowed_views", () => {
    const result = v.safeParse(CalendarViewDeclarationSchema, {
      ...base,
      color_map: { call: "#3B82F6" },
      allowed_views: ["month", "week"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts manifest-spec.md §9.4's own canonical example verbatim, explicit nulls included", () => {
    const result = v.safeParse(CalendarViewDeclarationSchema, {
      name: "activities_calendar",
      type: "calendar",
      resource: "contacts.activity",
      label: "Activity Calendar",
      date_field: "scheduled_at",
      end_date_field: null,
      title_field: "subject",
      color_field: "type",
      color_map: {},
      default_view: "month",
      allowed_views: ["month", "week", "day", "agenda"],
      filters: [],
      on_click: "contacts_form",
      on_date_click: null,
      quick_create: false,
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.end_date_field).toBeUndefined();
  });
});
