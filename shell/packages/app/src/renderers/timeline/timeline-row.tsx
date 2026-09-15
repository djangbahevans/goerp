import type { ReactNode } from "react";
import { TimelineBar } from "./timeline-bar.js";
import type { TimelineRowData } from "./timeline-view-types.js";

// timeline-chart.md: each lane is --space-8 (32px) tall with --space-1
// (4px) between stacked lanes within a row.
const LANE_HEIGHT_PX = 32;
const LANE_GAP_PX = 4;

export interface TimelineRowProps {
  row: TimelineRowData;
  range: { start: Date; end: Date };
  pxPerDay: number;
  allowDrag?: boolean | undefined;
  allowResize?: boolean | undefined;
}

// `contents` so the label/track cells become direct grid items of
// TimelineChart's own grid (sharing its two columns) rather than nesting a
// second grid inside a grid item — this is what lets the today-marker
// overlay span every row via a plain `grid-row: 1 / -1` placement.
//
// No role="row"/"gridcell" here — same posture Kanban's board/column/card
// and Calendar's own month grid already take (plain elements, individually
// labeled, focusable, and live-announced, rather than a full ARIA grid
// widget the rest of this codebase doesn't otherwise commit to).
export function TimelineRow({ row, range, pxPerDay, allowDrag, allowResize }: TimelineRowProps): ReactNode {
  const trackHeight = row.laneCount * LANE_HEIGHT_PX + Math.max(0, row.laneCount - 1) * LANE_GAP_PX;

  return (
    <div className="contents">
      <div className="truncate py-1 text-sm text-text-secondary">{row.label}</div>
      {/* Renders as a blank track — no inline EmptyState — when a group has
          no bars overlapping the visible range: a Gantt chart with several
          dozen rows would turn that into constant visual noise. */}
      <div className="relative" style={{ height: Math.max(trackHeight, LANE_HEIGHT_PX) }}>
        {row.bars.map((bar) => (
          <div
            key={bar.id}
            className="absolute w-full"
            style={{ top: bar.lane * (LANE_HEIGHT_PX + LANE_GAP_PX), height: LANE_HEIGHT_PX }}
          >
            <TimelineBar bar={bar} range={range} pxPerDay={pxPerDay} allowDrag={allowDrag} allowResize={allowResize} />
          </div>
        ))}
      </div>
    </div>
  );
}
