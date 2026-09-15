import { describe, expect, it } from "vitest";
import { buildOverlapFilter } from "./timeline-overlap-filter.js";

describe("buildOverlapFilter", () => {
  it("produces two independent field clauses — the bar's interval overlaps the window, not starts inside it", () => {
    const view = { start_field: "planned_start", end_field: "planned_end" };
    const range = { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) };
    expect(buildOverlapFilter(view, range)).toEqual({
      planned_start: { lte: "2026-05-31" },
      planned_end: { gte: "2026-05-01" },
    });
  });
});
