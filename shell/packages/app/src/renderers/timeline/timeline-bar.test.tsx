import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TimelineBar } from "./timeline-bar.js";
import type { TimelineBarData } from "./timeline-view-types.js";

afterEach(cleanup);

const range = { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) };

function makeBar(overrides: Partial<TimelineBarData> = {}): TimelineBarData {
  return {
    id: "t1",
    label: "Design review",
    start: new Date(2026, 4, 10),
    end: new Date(2026, 4, 14),
    clamped: false,
    lane: 0,
    ...overrides,
  };
}

describe("TimelineBar", () => {
  it("is a focusable role=group with an accessible name combining label and date range", () => {
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={10} />);
    const bar = screen.getByRole("group");
    expect(bar.tabIndex).toBe(0);
    expect(bar.getAttribute("aria-label")).toContain("Design review");
    expect(bar.getAttribute("aria-label")).toMatch(/\d/);
    expect(bar.getAttribute("title")).toBe("Design review");
  });

  it("renders the label inline when the bar is wide enough", () => {
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} />);
    expect(screen.getByText("Design review")).toBeTruthy();
  });

  it("drops the visible label below the rendered-width threshold, keeping title/aria-label", () => {
    render(<TimelineBar bar={makeBar({ end: new Date(2026, 4, 10) })} range={range} pxPerDay={1} />);
    expect(screen.queryByText("Design review")).toBeNull();
    expect(screen.getByRole("group").getAttribute("aria-label")).toContain("Design review");
  });

  it("applies the color_map fill as an inline background color", () => {
    render(<TimelineBar bar={makeBar({ color: "#3B82F6" })} range={range} pxPerDay={50} />);
    expect((screen.getByRole("group") as HTMLElement).style.backgroundColor).toBe("rgb(59, 130, 246)");
  });

  it("falls back to the --color-primary fill when no color is given", () => {
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} />);
    const bar = screen.getByRole("group");
    expect(bar.style.backgroundColor).toBe("");
    expect(bar.className).toContain("bg-primary");
  });
});
