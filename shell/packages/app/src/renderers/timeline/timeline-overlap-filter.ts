import type { FilterRange } from "@goerp/sdk";
import { dateKey } from "./timeline-date-utils.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";

// timeline-chart.md: fetching bars for the visible range is an *overlap*
// query, not Calendar's single-field range filter — a task that started
// last month and is still running must still show up viewing "this
// month," which a strict start_field >= range.start filter would
// incorrectly exclude. The correct fetch is two independent field clauses,
// ANDed by the engine: "the bar's own interval overlaps the visible
// window," not "the bar starts inside it."
export function buildOverlapFilter(
  view: Pick<TimelineViewDeclaration, "start_field" | "end_field">,
  range: { start: Date; end: Date },
): Record<string, FilterRange> {
  return {
    [view.start_field]: { lte: dateKey(range.end) },
    [view.end_field]: { gte: dateKey(range.start) },
  };
}
