import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";
import { TimelineChart } from "./timeline-chart.js";
import type { TimelineBarData, TimelineRowData } from "./timeline-view-types.js";

// TimelineChart's own today-marker reads the real wall-clock date, unlike
// CalendarView's initialDate prop — the marker only renders when today
// happens to fall inside whatever range a story sets.
const MONTH_RANGE = { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) };

// Alice's two tasks overlap (May 5–9 and May 8–12) — exercises sub-lane
// stacking within a single row. Bob's "Backend API" starts before the
// visible month and is still running through it, demonstrating the
// overlap-query fetch's whole point: it isn't dropped just because it
// doesn't start inside the visible window.
const ROWS: TimelineRowData[] = [
  {
    id: "alice",
    label: "Alice",
    laneCount: 2,
    bars: [
      {
        id: "task-1",
        label: "Design review",
        start: new Date(2026, 4, 5),
        end: new Date(2026, 4, 9),
        color: "#EF4444",
        clamped: false,
        lane: 0,
      },
      {
        id: "task-2",
        label: "Spec doc",
        start: new Date(2026, 4, 8),
        end: new Date(2026, 4, 12),
        color: "#F59E0B",
        clamped: false,
        lane: 1,
      },
    ],
  },
  {
    id: "bob",
    label: "Bob",
    laneCount: 1,
    bars: [
      {
        id: "task-3",
        label: "Backend API",
        start: new Date(2026, 3, 20),
        end: new Date(2026, 4, 15),
        color: "#EF4444",
        clamped: false,
        lane: 0,
      },
    ],
  },
  {
    id: "carol",
    label: "Carol",
    laneCount: 1,
    bars: [
      {
        id: "task-4",
        label: "Frontend polish",
        start: new Date(2026, 4, 20),
        end: new Date(2026, 4, 25),
        color: "#10B981",
        clamped: false,
        lane: 0,
      },
    ],
  },
];

const meta: Meta<typeof TimelineChart> = {
  title: "Renderers/TimelineChart",
  component: TimelineChart,
  args: {
    rows: ROWS,
    range: MONTH_RANGE,
    rangeMode: "month",
    onRangeModeChange: () => {},
    onNavigate: () => {},
    onBarChange: async () => {},
    label: "Project Timeline",
    allowDrag: true,
    allowResize: true,
  },
};

export default meta;

type Story = StoryObj<typeof TimelineChart>;

export const MonthView: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("group", { name: /Design review/ })).toBeInTheDocument();
    await expect(canvas.getByRole("group", { name: /Backend API/ })).toBeInTheDocument();
    await expect(canvas.getByText("Alice")).toBeInTheDocument();
    await expect(canvas.getAllByRole("slider")).toHaveLength(ROWS.flatMap((r) => r.bars).length * 2);
  },
};

const WEEK_RANGE = { start: new Date(2026, 4, 10), end: new Date(2026, 4, 16) };

// A real fetch for this narrower range would come back overlap-filtered
// server-side — "Design review" (ends May 9) and "Frontend polish" (starts
// May 20) don't overlap May 10–16 at all, so they're absent here, the same
// as TimelineRenderer's own overlap query would leave them out.
const WEEK_ROWS: TimelineRowData[] = [
  { id: "alice", label: "Alice", laneCount: 1, bars: [{ ...(ROWS[0]?.bars[1] as TimelineBarData), lane: 0 }] },
  { id: "bob", label: "Bob", laneCount: 1, bars: ROWS[1]?.bars ?? [] },
];

export const WeekView: Story = {
  args: { rangeMode: "week", range: WEEK_RANGE, rows: WEEK_ROWS },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("tab", { name: "Week", selected: true })).toBeInTheDocument();
    await expect(canvas.getByRole("group", { name: /Spec doc/ })).toBeInTheDocument();
    await expect(canvas.queryByText("Design review")).not.toBeInTheDocument();
    await expect(canvas.queryByText("Frontend polish")).not.toBeInTheDocument();
  },
};

export const ReadOnly: Story = {
  name: "allow_drag/allow_resize both false",
  args: { allowDrag: false, allowResize: false },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("group", { name: /Design review/ })).toBeInTheDocument();
    await expect(canvas.queryAllByRole("slider")).toHaveLength(0);
  },
};

export const SwitchingRangeMode: Story = {
  name: "clicking a range tab reports the new mode",
  // TimelineChart is a controlled component (rangeMode comes from props, as
  // TimelineRenderer's own state) — clicking a tab here can only be
  // observed via the callback, not a re-rendered selected tab.
  args: { onRangeModeChange: fn() },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("tab", { name: "Quarter" }));
    await expect(args.onRangeModeChange).toHaveBeenCalledWith("quarter");
  },
};
