import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
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

  it("clips a bar starting before the visible range to the track's left edge", () => {
    render(<TimelineBar bar={makeBar({ start: new Date(2026, 3, 1) })} range={range} pxPerDay={10} />);
    expect((screen.getByRole("group") as HTMLElement).style.left).toBe("0px");
  });

  it("clips a bar ending after the visible range to the track's right edge", () => {
    render(
      <TimelineBar
        bar={makeBar({ start: new Date(2026, 4, 25), end: new Date(2026, 5, 10) })}
        range={range}
        pxPerDay={10}
      />,
    );
    const style = (screen.getByRole("group") as HTMLElement).style;
    // range is May 1–31 inclusive: 31 days * 10px/day = 310px track width.
    expect(Number.parseFloat(style.left) + Number.parseFloat(style.width)).toBeLessThanOrEqual(310);
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

  it("renders two resize handles only when allowResize", () => {
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} allowResize={true} />);
    expect(screen.getAllByRole("slider")).toHaveLength(2);
  });

  it("renders no resize handles when allowResize is false", () => {
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} allowResize={false} />);
    expect(screen.queryAllByRole("slider")).toHaveLength(0);
  });

  it("keyboard: ArrowRight on the bar nudges by one day and announces via onNudge", () => {
    const onNudge = vi.fn();
    const bar = makeBar();
    render(<TimelineBar bar={bar} range={range} pxPerDay={50} onNudge={onNudge} />);
    fireEvent.keyDown(screen.getByRole("group"), { key: "ArrowRight" });
    expect(onNudge).toHaveBeenCalledWith("move", 1, { start: bar.start, end: bar.end });
  });

  it("keyboard: PageDown on the bar nudges by one week", () => {
    const onNudge = vi.fn();
    const bar = makeBar();
    render(<TimelineBar bar={bar} range={range} pxPerDay={50} onNudge={onNudge} />);
    fireEvent.keyDown(screen.getByRole("group"), { key: "PageDown" });
    expect(onNudge).toHaveBeenCalledWith("move", 7, { start: bar.start, end: bar.end });
  });

  it("keyboard: ArrowRight on the bar is a no-op when allowDrag is false", () => {
    const onNudge = vi.fn();
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} allowDrag={false} onNudge={onNudge} />);
    fireEvent.keyDown(screen.getByRole("group"), { key: "ArrowRight" });
    expect(onNudge).not.toHaveBeenCalled();
  });

  it("keyboard: Enter on the bar fires onCommit, Escape fires onCancel", () => {
    const onCommit = vi.fn();
    const onCancel = vi.fn();
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} onCommit={onCommit} onCancel={onCancel} />);
    fireEvent.keyDown(screen.getByRole("group"), { key: "Enter" });
    fireEvent.keyDown(screen.getByRole("group"), { key: "Escape" });
    expect(onCommit).toHaveBeenCalledTimes(1);
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("keyboard: Shift+ArrowRight on a resize handle nudges only that edge, not plain ArrowRight", () => {
    const onNudge = vi.fn();
    const bar = makeBar();
    render(<TimelineBar bar={bar} range={range} pxPerDay={50} onNudge={onNudge} />);
    const [startHandle] = screen.getAllByRole("slider");
    fireEvent.keyDown(startHandle as HTMLElement, { key: "ArrowRight" });
    expect(onNudge).not.toHaveBeenCalled();
    fireEvent.keyDown(startHandle as HTMLElement, { key: "ArrowRight", shiftKey: true });
    expect(onNudge).toHaveBeenCalledWith("resize-start", 1, { start: bar.start, end: bar.end });
  });

  it("keyboard: a resize handle nudge is a no-op when allowResize is false", () => {
    const onNudge = vi.fn();
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} allowResize={false} onNudge={onNudge} />);
    expect(screen.queryAllByRole("slider")).toHaveLength(0);
  });

  it("renders the live floating date label and reduced opacity while projected (dragging/nudging)", () => {
    const bar = makeBar();
    const projected = { start: new Date(2026, 4, 12), end: new Date(2026, 4, 16) };
    render(<TimelineBar bar={bar} range={range} pxPerDay={50} projected={projected} />);
    expect(screen.getByRole("group").className).toContain("opacity-85");
    expect(screen.getByText(/May 12.*May 16/)).toBeTruthy();
  });

  it("pointer drag: pointerdown on the bar body starts a move drag with the bar's current dates", () => {
    const onDragStart = vi.fn();
    const bar = makeBar();
    render(<TimelineBar bar={bar} range={range} pxPerDay={50} onDragStart={onDragStart} />);
    const el = screen.getByRole("group") as HTMLElement & { setPointerCapture: (id: number) => void };
    el.setPointerCapture = vi.fn();
    fireEvent.pointerDown(el, { clientX: 100, pointerId: 1 });
    expect(onDragStart).toHaveBeenCalledWith("move", 100, { start: bar.start, end: bar.end });
  });

  it("pointer drag: a move pointerdown is a no-op when allowDrag is false", () => {
    const onDragStart = vi.fn();
    render(<TimelineBar bar={makeBar()} range={range} pxPerDay={50} allowDrag={false} onDragStart={onDragStart} />);
    fireEvent.pointerDown(screen.getByRole("group"), { clientX: 100, pointerId: 1 });
    expect(onDragStart).not.toHaveBeenCalled();
  });
});
