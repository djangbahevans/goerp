import { describe, expect, it } from "vitest";
import { assignLanes, buildTimelineRows } from "./timeline-layout.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";

const baseView: TimelineViewDeclaration = {
  name: "project_timeline",
  type: "timeline",
  resource: "project.task",
  label: "Timeline",
  start_field: "planned_start",
  end_field: "planned_end",
  label_field: "name",
};

describe("buildTimelineRows", () => {
  it("drops a row missing start_field or end_field", () => {
    const rows = [
      { id: "t1", name: "No start", planned_end: "2026-05-10" },
      { id: "t2", name: "No end", planned_start: "2026-05-10" },
    ];
    expect(buildTimelineRows(rows, baseView)).toEqual([]);
  });

  it("clamps an inverted range to a one-day bar anchored at start_field", () => {
    const rows = [{ id: "t1", name: "Backwards", planned_start: "2026-05-10", planned_end: "2026-05-05" }];
    const [row] = buildTimelineRows(rows, baseView);
    const bar = row?.bars[0];
    expect(bar?.clamped).toBe(true);
    expect(bar?.start).toEqual(new Date("2026-05-10"));
    // Inclusive end dates (timeline-bar.tsx renders width through end+1
    // day) mean "one day wide" is end === start, not start+1 (which would
    // render as two days).
    expect(bar?.end).toEqual(new Date("2026-05-10"));
  });

  it("buckets every row into a single implicit row, labeled '', when group_by is absent", () => {
    const rows = [
      { id: "t1", name: "A", planned_start: "2026-05-01", planned_end: "2026-05-02" },
      { id: "t2", name: "B", planned_start: "2026-05-03", planned_end: "2026-05-04" },
    ];
    const result = buildTimelineRows(rows, baseView);
    expect(result).toHaveLength(1);
    expect(result[0]?.label).toBe("");
    expect(result[0]?.bars).toHaveLength(2);
  });

  it("labels a row missing its group_by value 'Unassigned' when group_by is declared", () => {
    const view = { ...baseView, group_by: "assignee_id" };
    const rows = [
      { id: "t1", name: "A", planned_start: "2026-05-01", planned_end: "2026-05-02" },
      { id: "t2", name: "B", planned_start: "2026-05-01", planned_end: "2026-05-02", assignee_id: "u1" },
    ];
    const result = buildTimelineRows(rows, view);
    expect(result.map((r) => r.label).sort()).toEqual(["Unassigned", "u1"]);
  });

  it("resolves color via color_field/color_map, falling back to no color", () => {
    const view = { ...baseView, color_field: "priority", color_map: { high: "#FF0000" } };
    const rows = [
      { id: "t1", name: "A", planned_start: "2026-05-01", planned_end: "2026-05-02", priority: "high" },
      { id: "t2", name: "B", planned_start: "2026-05-01", planned_end: "2026-05-02", priority: "low" },
    ];
    const [row] = buildTimelineRows(rows, view);
    const a = row?.bars.find((b) => b.id === "t1");
    const b = row?.bars.find((b) => b.id === "t2");
    expect(a?.color).toBe("#FF0000");
    expect(b?.color).toBeUndefined();
  });
});

describe("assignLanes", () => {
  it("stacks overlapping bars into increasing lanes, and reuses a freed lane", () => {
    const bars = [
      { id: "a", start: new Date(2026, 4, 1), end: new Date(2026, 4, 10) },
      { id: "b", start: new Date(2026, 4, 5), end: new Date(2026, 4, 15) },
      { id: "c", start: new Date(2026, 4, 8), end: new Date(2026, 4, 20) },
      { id: "d", start: new Date(2026, 4, 11), end: new Date(2026, 4, 25) },
    ];
    const lanes = assignLanes(bars);
    expect(lanes.get("a")).toBe(0);
    expect(lanes.get("b")).toBe(1);
    expect(lanes.get("c")).toBe(2);
    // "d" starts after "a" ends — reuses lane 0 rather than opening a 4th lane.
    expect(lanes.get("d")).toBe(0);
  });

  it("ties by record id ascending when start_field is identical", () => {
    const bars = [
      { id: "z", start: new Date(2026, 4, 1), end: new Date(2026, 4, 5) },
      { id: "a", start: new Date(2026, 4, 1), end: new Date(2026, 4, 5) },
    ];
    const lanes = assignLanes(bars);
    expect(lanes.get("a")).toBe(0);
    expect(lanes.get("z")).toBe(1);
  });

  it("treats a shared boundary day as overlapping — end dates are inclusive", () => {
    const bars = [
      { id: "a", start: new Date(2026, 4, 1), end: new Date(2026, 4, 5) },
      // "b" starts the same day "a" ends — they still share that day's
      // pixels on screen, so they must not land in the same lane.
      { id: "b", start: new Date(2026, 4, 5), end: new Date(2026, 4, 10) },
    ];
    const lanes = assignLanes(bars);
    expect(lanes.get("a")).not.toBe(lanes.get("b"));
  });
});
