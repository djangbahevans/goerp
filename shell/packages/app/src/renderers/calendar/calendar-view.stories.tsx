import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { CalendarView } from "./calendar-view.js";
import type { CalendarEvent } from "./calendar-view-types.js";

// Wednesday, May 13 2026 — fixed so "today"'s ring and the initially-visible
// range don't drift with real wall-clock time between Storybook runs.
const REFERENCE_DATE = new Date(2026, 4, 13);

// 5 events on the same day: 3 stack as chips, the remaining 2 collapse into
// "+2 more" (view-system.md's month-grid AC). One uses a deliberately
// near-white fill ("#FAFAFA") to exercise the runtime contrast computation's
// fill-vs-surface outline fallback, not just the text-color branch.
const BUSY_DAY_EVENTS: CalendarEvent[] = [
  {
    id: "1",
    title: "Team standup",
    start: new Date(2026, 4, 13, 9, 0),
    end: new Date(2026, 4, 13, 9, 30),
    color: "#3B82F6",
  },
  {
    id: "2",
    title: "Design review",
    start: new Date(2026, 4, 13, 9, 15),
    end: new Date(2026, 4, 13, 10, 15),
    color: "#8B5CF6",
  },
  {
    id: "3",
    title: "Low-contrast demo",
    start: new Date(2026, 4, 13, 11, 0),
    end: new Date(2026, 4, 13, 11, 30),
    color: "#FAFAFA",
  },
  {
    id: "4",
    title: "Client call",
    start: new Date(2026, 4, 13, 13, 0),
    end: new Date(2026, 4, 13, 14, 0),
    color: "#10B981",
  },
  {
    id: "5",
    title: "Submit expense report",
    start: new Date(2026, 4, 13, 16, 0),
    end: null,
    color: "#F59E0B",
  },
];

const OTHER_EVENTS: CalendarEvent[] = [
  {
    id: "6",
    title: "Quarterly planning offsite",
    start: new Date(2026, 4, 11, 0, 0),
    end: new Date(2026, 4, 12, 0, 0),
    color: "#8B5CF6",
  },
  {
    id: "7",
    title: "1:1 with manager",
    start: new Date(2026, 4, 15, 10, 0),
    end: new Date(2026, 4, 15, 10, 30),
    color: "#3B82F6",
  },
];

const SAMPLE_EVENTS: CalendarEvent[] = [...BUSY_DAY_EVENTS, ...OTHER_EVENTS];

const meta: Meta<typeof CalendarView> = {
  title: "Renderers/CalendarView",
  component: CalendarView,
  args: {
    events: SAMPLE_EVENTS,
    initialDate: REFERENCE_DATE,
    quickCreate: true,
  },
};

export default meta;

type Story = StoryObj<typeof CalendarView>;

export const MonthView: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("table", { name: "Month" })).toBeInTheDocument();
    await expect(canvas.getByText("+2 more")).toBeInTheDocument();
  },
};

export const WeekView: Story = {
  args: { defaultView: "week" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("table", { name: "Schedule" })).toBeInTheDocument();
    await expect(canvas.getByText("Team standup")).toBeInTheDocument();
  },
};

export const DayView: Story = {
  args: { defaultView: "day" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("table", { name: "Schedule" })).toBeInTheDocument();
    await expect(canvas.getByText("Client call")).toBeInTheDocument();
  },
};

export const AgendaView: Story = {
  args: { defaultView: "agenda" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("1:1 with manager")).toBeInTheDocument();
  },
};

export const RestrictedViews: Story = {
  name: "Restricted allowedViews (month + agenda only)",
  args: { allowedViews: ["month", "agenda"] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("tab", { name: "Month" })).toBeInTheDocument();
    await expect(canvas.getByRole("tab", { name: "Agenda" })).toBeInTheDocument();
    await expect(canvas.queryByRole("tab", { name: "Week" })).not.toBeInTheDocument();
  },
};
