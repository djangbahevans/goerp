// Presentational props for TimelineChart and its sub-components —
// already-resolved rows/bars, not manifest-spec.md §9.6's manifest fields
// (start_field/end_field/group_by/label_field/color_field/color_map),
// which timeline-layout.ts resolves.

export type TimelineRangeMode = "week" | "month" | "quarter" | "year";

// The one shared source for "every supported range" — TimelineChart's own
// default and timeline-date-utils.ts's initialRangeMode both fall back to
// this, mirroring calendar-view-types.ts's ALL_CALENDAR_VIEWS.
export const ALL_TIMELINE_RANGES: TimelineRangeMode[] = ["week", "month", "quarter", "year"];

export interface TimelineBarData {
  id: string;
  label: string;
  start: Date;
  end: Date;
  // Resolved color_map[color_field value] hex, e.g. "#3B82F6". Undefined
  // renders with the --color-primary default fill.
  color?: string | undefined;
  // end_field < start_field was clamped to a minimum one-day bar anchored
  // at start_field, per timeline-chart.md's edge-case rule — not a visual
  // state, but callers (tests, tooltips) may want to know.
  clamped: boolean;
  // Assigned by timeline-layout.ts's assignLanes — which sub-lane within
  // its row this bar stacks into when it overlaps a sibling in time.
  lane: number;
}

export interface TimelineRowData {
  id: string;
  // "Unassigned" for an absent/null/empty group_by value, the raw
  // String(value) otherwise — no group_label_field/relation lookup exists
  // in the manifest schema for Timeline, unlike Kanban's group_by.
  label: string;
  bars: TimelineBarData[];
  laneCount: number;
}

// The one shape both pointer-drag and keyboard commits produce, feeding a
// single onBarChange callback — mirrors Kanban's single onMoveCard shape.
export interface TimelineBarChange {
  id: string;
  start: Date;
  end: Date;
}

export interface TimelineChartProps {
  rows: TimelineRowData[];
  range: { start: Date; end: Date };
  rangeMode: TimelineRangeMode;
  onRangeModeChange: (mode: TimelineRangeMode) => void;
  onNavigate: (direction: -1 | 0 | 1) => void;
  // manifest-spec.md §9.6's allow_drag/allow_resize (both default true) —
  // disables the interaction entirely rather than just rejecting the
  // resulting onBarChange call, same posture as Kanban's allowDrag.
  allowDrag?: boolean | undefined;
  allowResize?: boolean | undefined;
  onBarChange: (change: TimelineBarChange) => Promise<void>;
  // The view's own manifest `label` — renders as aria-label="Timeline:
  // {label}" on the chart, per timeline-chart.md's Accessibility section.
  label: string;
}
