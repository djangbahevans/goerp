import type { Row } from "../list/list-view-types.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";
import type { TimelineBarData, TimelineRowData } from "./timeline-view-types.js";

function toDate(value: unknown): Date | null {
  if (typeof value !== "string" && typeof value !== "number") return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

// end_field before start_field is a data error, not a normal case — clamps
// to a minimum one-day-wide bar anchored at start_field, per
// timeline-chart.md, rather than rendering a negative-width bar or
// dropping an otherwise-valid record.
function clampBarRange(start: Date, end: Date): { start: Date; end: Date; clamped: boolean } {
  if (end.getTime() >= start.getTime()) return { start, end, clamped: false };
  const clampedEnd = new Date(start);
  clampedEnd.setDate(clampedEnd.getDate() + 1);
  return { start, end: clampedEnd, clamped: true };
}

// A single sentinel bucket key covers both "no group_by declared at all"
// (every row shares it) and "group_by declared but this row's value is
// absent/null/empty" — labelForGroupKey tells the two apart via hasGroupBy.
function groupKey(row: Row, groupBy: string | undefined): string {
  if (!groupBy) return "";
  const value = row[groupBy];
  if (value === null || value === undefined || value === "") return "";
  return String(value);
}

// No group_label_field exists in Timeline's manifest schema (unlike
// Kanban's group_by, which has one) — group_by's raw value is the row
// label directly, never a relation lookup. "Unassigned" only applies when
// a group_by field is actually declared and a row's value is missing.
function labelForGroupKey(key: string, hasGroupBy: boolean): string {
  if (!hasGroupBy) return "";
  return key === "" ? "Unassigned" : key;
}

// Greedy first-fit over a deterministic sort (start_field ascending, tied
// by record id) — stable across refetches with no cache needed, since lane
// assignment is a pure function of the overlapping-bar set for a group. A
// bar only changes lanes when that set itself changes (entered/left the
// visible range, or its own dates changed), which is correct behavior, not
// something to suppress.
export function assignLanes<T extends { id: string; start: Date; end: Date }>(bars: T[]): Map<string, number> {
  const sorted = [...bars].sort((a, b) => a.start.getTime() - b.start.getTime() || a.id.localeCompare(b.id));
  const laneEnds: number[] = [];
  const lanes = new Map<string, number>();
  for (const bar of sorted) {
    const laneIndex = laneEnds.findIndex((end) => end <= bar.start.getTime());
    const lane = laneIndex === -1 ? laneEnds.length : laneIndex;
    laneEnds[lane] = bar.end.getTime();
    lanes.set(bar.id, lane);
  }
  return lanes;
}

function resolveColor(row: Row, view: Pick<TimelineViewDeclaration, "color_field" | "color_map">): string | undefined {
  if (!view.color_field) return undefined;
  const colorKey = row[view.color_field];
  return view.color_map && typeof colorKey === "string" ? view.color_map[colorKey] : undefined;
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
    const color = resolveColor(row, view);
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
    return { id: key, label: labelForGroupKey(key, view.group_by !== undefined), bars: laidOutBars, laneCount };
  });
}
