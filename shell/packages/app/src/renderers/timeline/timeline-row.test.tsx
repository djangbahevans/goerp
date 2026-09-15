import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TimelineRow } from "./timeline-row.js";
import type { TimelineRowData } from "./timeline-view-types.js";

afterEach(cleanup);

const range = { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) };

describe("TimelineRow", () => {
  it("renders a blank track, with no EmptyState message, when a group has no bars in range", () => {
    const row: TimelineRowData = { id: "g1", label: "Unassigned", bars: [], laneCount: 0 };
    render(<TimelineRow row={row} range={range} pxPerDay={10} />);
    expect(screen.getByText("Unassigned")).toBeTruthy();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("renders one TimelineBar per bar in the row", () => {
    const row: TimelineRowData = {
      id: "g1",
      label: "Alice",
      bars: [
        { id: "t1", label: "Task A", start: new Date(2026, 4, 5), end: new Date(2026, 4, 8), clamped: false, lane: 0 },
        { id: "t2", label: "Task B", start: new Date(2026, 4, 9), end: new Date(2026, 4, 12), clamped: false, lane: 1 },
      ],
      laneCount: 2,
    };
    render(<TimelineRow row={row} range={range} pxPerDay={10} />);
    expect(screen.getAllByRole("group")).toHaveLength(2);
  });
});
