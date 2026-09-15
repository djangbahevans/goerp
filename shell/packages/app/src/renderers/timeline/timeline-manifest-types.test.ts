import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { TimelineViewDeclarationSchema } from "./timeline-manifest-types.js";

const base = {
  name: "project_timeline",
  type: "timeline",
  resource: "project.task",
  label: "Timeline",
  start_field: "planned_start",
  end_field: "planned_end",
  label_field: "name",
};

describe("TimelineViewDeclarationSchema", () => {
  it("accepts a minimal valid declaration", () => {
    expect(v.safeParse(TimelineViewDeclarationSchema, base).success).toBe(true);
  });

  it("requires start_field, end_field, and label_field", () => {
    const { start_field: _start, ...withoutStart } = base;
    expect(v.safeParse(TimelineViewDeclarationSchema, withoutStart).success).toBe(false);
    const { end_field: _end, ...withoutEnd } = base;
    expect(v.safeParse(TimelineViewDeclarationSchema, withoutEnd).success).toBe(false);
    const { label_field: _label, ...withoutLabel } = base;
    expect(v.safeParse(TimelineViewDeclarationSchema, withoutLabel).success).toBe(false);
  });

  it("group_by/default_range are optional", () => {
    expect(v.safeParse(TimelineViewDeclarationSchema, base).success).toBe(true);
  });

  it("rejects a default_range outside week/month/quarter/year", () => {
    const result = v.safeParse(TimelineViewDeclarationSchema, { ...base, default_range: "day" });
    expect(result.success).toBe(false);
  });

  it("accepts manifest-spec.md §9.6's own canonical example verbatim", () => {
    const result = v.safeParse(TimelineViewDeclarationSchema, {
      name: "project_timeline",
      type: "timeline",
      resource: "project.task",
      label: "Timeline",
      start_field: "planned_start",
      end_field: "planned_end",
      group_by: "assignee_id",
      label_field: "name",
      color_field: "priority",
      color_map: {},
      filters: [],
      allow_drag: true,
      allow_resize: true,
      default_range: "month",
    });
    expect(result.success).toBe(true);
  });
});
