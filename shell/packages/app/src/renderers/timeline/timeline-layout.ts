import { toDate } from "../calendar/calendar-date-utils.js";
import { resolveMappedColor } from "../calendar/contrast.js";
import type { Row } from "../list/list-view-types.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import type { TimelineBarData, TimelineRowData } from "./timeline-view-types.js";

// Per timeline-chart.md: an inverted range clamps to a one-day bar at
// start_field. End is inclusive (see timeline-bar.tsx), so that's end===start.
function clampBarRange(start: Date, end: Date): { start: Date; end: Date; clamped: boolean } {
  if (end.getTime() >= start.getTime()) return { start, end, clamped: false };
  return { start, end: start, clamped: true };
}

// A single sentinel bucket key covers both "no group_by declared at all"
// (every row shares it) and "group_by declared but this row's value is
// absent/null/empty" — the caller tells the two apart via view.group_by.
function groupKey(row: Row, groupBy: string | undefined): string {
  if (!groupBy) return "";
  const value = row[groupBy];
  if (value === null || value === undefined || value === "") return "";
  return String(value);
}

// Greedy first-fit, sorted by start_field then id for a stable result
// across refetches. End dates are inclusive, so a lane frees up only once
// its occupant's end is strictly before the next bar's start.
export function assignLanes<T extends { id: string; start: Date; end: Date }>(bars: T[]): Map<string, number> {
  const sorted = [...bars].sort((a, b) => a.start.getTime() - b.start.getTime() || a.id.localeCompare(b.id));
  const laneEnds: number[] = [];
  const lanes = new Map<string, number>();
  for (const bar of sorted) {
    const laneIndex = laneEnds.findIndex((end) => end < bar.start.getTime());
    const lane = laneIndex === -1 ? laneEnds.length : laneIndex;
    laneEnds[lane] = bar.end.getTime();
    lanes.set(bar.id, lane);
  }
  return lanes;
}

// Orchestrates: drop rows missing start_field/end_field, clamp inverted
// ranges, bucket by group, stack overlapping bars into sub-lanes per
// group — timeline-chart.md's full edge-case list.
export function buildTimelineRows(rows: Row[], view: TimelineViewDeclaration): TimelineRowData[] {
  const buckets = new Map<string, TimelineBarData[]>();
  const bucketOrder: string[] = [];

  for (const row of rows) {
    const rawStart = toDate(row[view.start_field]);
    const rawEnd = toDate(row[view.end_field]);
    if (!rawStart || !rawEnd) continue;

    const { start, end, clamped } = clampBarRange(rawStart, rawEnd);
    const id = String(row.id ?? "");
    const label = String(row[view.label_field] ?? "");
    const color = resolveMappedColor(view.color_field ? row[view.color_field] : undefined, view.color_map);
    const key = groupKey(row, view.group_by);

    let bucket = buckets.get(key);
    if (!bucket) {
      bucket = [];
      buckets.set(key, bucket);
      bucketOrder.push(key);
    }
    bucket.push({ id, label, start, end, clamped, lane: 0, ...(color ? { color } : {}) });
  }

  return bucketOrder.map((key) => {
    const bars = buckets.get(key) as TimelineBarData[];
    const lanes = assignLanes(bars);
    const laidOutBars = bars.map((bar) => ({ ...bar, lane: lanes.get(bar.id) ?? 0 }));
    const laneCount = new Set(laidOutBars.map((bar) => bar.lane)).size;
    // No group_label_field exists in Timeline's manifest schema (unlike
    // Kanban's group_by) — the raw group_by value is the row label
    // directly, never a relation lookup. "Unassigned" only applies when a
    // group_by field is actually declared and a row's value is missing.
    const label = view.group_by === undefined ? "" : key === "" ? "Unassigned" : key;
    return { id: key, label, bars: laidOutBars, laneCount };
  });
}
