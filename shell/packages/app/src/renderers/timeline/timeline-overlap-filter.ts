import type { FilterRange } from "@goerp/sdk";
import { dateKey } from "./timeline-date-utils.js";
import type { TimelineViewDeclaration } from "./timeline-manifest-types.js";

// An overlap query, not Calendar's single-field range filter — see
// timeline-chart.md's "Fetching bars for the visible range" rule.
export function buildOverlapFilter(
  view: Pick<TimelineViewDeclaration, "start_field" | "end_field">,
  range: { start: Date; end: Date },
): Record<string, FilterRange> {
  return {
    [view.start_field]: { lte: dateKey(range.end) },
    [view.end_field]: { gte: dateKey(range.start) },
  };
}
