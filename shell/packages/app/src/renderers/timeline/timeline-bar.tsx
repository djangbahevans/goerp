import { useTheme } from "@goerp/sdk/react";
import type { CSSProperties, ReactNode } from "react";
import { contrastFor } from "../calendar/contrast.js";
import { addDays, dateToX, formatDateRange } from "./timeline-date-utils.js";
import type { TimelineBarData } from "./timeline-view-types.js";

// Below this rendered width, label_field text wouldn't fit without
// truncating into an unreadable one- or two-character sliver —
// timeline-chart.md: dropped from the bar itself, kept only in `title`/the
// accessible name, never truncated illegibly.
const LABEL_MIN_WIDTH_PX = 60;
// timeline-chart.md: a bar never renders narrower than --space-3 (12px)
// regardless of its actual date span.
const MIN_BAR_WIDTH_PX = 12;

export interface TimelineBarProps {
  bar: TimelineBarData;
  range: { start: Date; end: Date };
  pxPerDay: number;
  allowDrag?: boolean | undefined;
  allowResize?: boolean | undefined;
}

export function TimelineBar({ bar, range, pxPerDay }: TimelineBarProps): ReactNode {
  const { theme } = useTheme();
  const contrast = bar.color ? contrastFor(bar.color, theme) : null;

  const left = dateToX(bar.start, range, pxPerDay);
  const rawWidth = dateToX(addDays(bar.end, 1), range, pxPerDay) - left;
  const width = Math.max(rawWidth, MIN_BAR_WIDTH_PX);
  const showLabel = width >= LABEL_MIN_WIDTH_PX;

  const style: CSSProperties = {
    left,
    width,
    ...(bar.color ? { backgroundColor: bar.color } : {}),
    ...(contrast?.text.kind === "literal" ? { color: contrast.text.hex } : {}),
  };
  const colorClassName = bar.color
    ? contrast?.text.kind === "token"
      ? contrast.text.className
      : ""
    : "bg-primary text-text-inverse";
  const accessibleName = `${bar.label}, ${formatDateRange(bar.start, bar.end)}`;

  return (
    // biome-ignore lint/a11y/useSemanticElements: role="group" marks a real keyboard-operable move/resize widget, not a form's <fieldset> grouping.
    <div
      role="group"
      // biome-ignore lint/a11y/noNoninteractiveTabindex: this "group" is a real composite widget (pointer-drag, Arrow/PageUp/PageDown/Enter/Escape) and must be a genuine Tab stop.
      tabIndex={0}
      title={bar.label}
      aria-label={accessibleName}
      style={style}
      className={`absolute top-0 h-full truncate rounded-control px-3 text-left text-sm transition-shadow duration-(--duration-fast) ease-out hover:shadow-sm focus-visible:shadow-focus focus-visible:outline-none ${colorClassName} ${
        contrast?.needsOutline ? "border border-border" : ""
      }`}
    >
      {showLabel && bar.label}
    </div>
  );
}
