import { useTheme } from "@goerp/sdk/react";
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
  PointerEvent as ReactPointerEvent,
} from "react";
import { contrastFor } from "../calendar/contrast.js";
import { addDays, dateToX, formatDate, formatDateRange } from "./timeline-date-utils.js";
import type { TimelineBarData, TimelineDragKind } from "./timeline-view-types.js";

// See timeline-chart.md's "Minimum bar width" and label-drop rules.
const LABEL_MIN_WIDTH_PX = 60;
const MIN_BAR_WIDTH_PX = 12;
// See timeline-chart.md's resize-handle hit-area rule (WCAG 2.5.5).
const RESIZE_HANDLE_HIT_AREA_PX = 48;

export interface TimelineBarProps {
  bar: TimelineBarData;
  range: { start: Date; end: Date };
  pxPerDay: number;
  allowDrag?: boolean | undefined;
  allowResize?: boolean | undefined;
  // Live projected dates while this bar is the one being pointer-dragged or
  // keyboard-nudged — overrides bar.start/end for rendering position and
  // the floating date label; absent otherwise.
  projected?: { start: Date; end: Date } | undefined;
  onDragStart?: ((kind: TimelineDragKind, clientX: number, current: { start: Date; end: Date }) => void) | undefined;
  onDragMove?: ((clientX: number) => void) | undefined;
  onDragEnd?: (() => void) | undefined;
  onNudge?: ((target: TimelineDragKind, deltaDays: number, current: { start: Date; end: Date }) => void) | undefined;
  onCommit?: (() => void) | undefined;
  onCancel?: (() => void) | undefined;
}

export function TimelineBar({
  bar,
  range,
  pxPerDay,
  allowDrag = true,
  allowResize = true,
  projected,
  onDragStart,
  onDragMove,
  onDragEnd,
  onNudge,
  onCommit,
  onCancel,
}: TimelineBarProps): ReactNode {
  const { theme } = useTheme();
  const contrast = bar.color ? contrastFor(bar.color, theme) : null;
  const isDragging = projected !== undefined;
  const displayStart = projected?.start ?? bar.start;
  const displayEnd = projected?.end ?? bar.end;

  const left = dateToX(displayStart, range, pxPerDay);
  const rawWidth = dateToX(addDays(displayEnd, 1), range, pxPerDay) - left;
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
  const accessibleName = `${bar.label}, ${formatDateRange(displayStart, displayEnd)}`;

  // Day-offset from range.start, shared by valuemin/valuemax/valuenow;
  // widened to the bar's own start/end since a bar routinely extends past
  // the visible range and valuenow must stay within [valuemin, valuemax].
  const rangeStartOffset = 0;
  const rangeEndOffset = Math.round((range.end.getTime() - range.start.getTime()) / 86_400_000);
  const startOffset = Math.round((displayStart.getTime() - range.start.getTime()) / 86_400_000);
  const endOffset = Math.round((displayEnd.getTime() - range.start.getTime()) / 86_400_000);
  const valueMin = Math.min(rangeStartOffset, startOffset, endOffset);
  const valueMax = Math.max(rangeEndOffset, startOffset, endOffset);

  function handlePointerDown(kind: TimelineDragKind) {
    return (event: ReactPointerEvent<HTMLDivElement>): void => {
      const allowed = kind === "move" ? allowDrag : allowResize;
      if (!allowed) return;
      event.stopPropagation();
      event.currentTarget.setPointerCapture(event.pointerId);
      onDragStart?.(kind, event.clientX, { start: bar.start, end: bar.end });
    };
  }

  function handlePointerMove(event: ReactPointerEvent<HTMLDivElement>): void {
    // A resize handle's captured pointer still bubbles through this same
    // handler on the bar's own outer div — stop it there, or a resize drag
    // would report every move twice.
    event.stopPropagation();
    onDragMove?.(event.clientX);
  }

  function handlePointerUp(event: ReactPointerEvent<HTMLDivElement>): void {
    event.stopPropagation();
    onDragEnd?.();
  }

  function handleBarKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Enter") {
      event.preventDefault();
      onCommit?.();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      onCancel?.();
      return;
    }
    if (!allowDrag) return;
    const current = { start: bar.start, end: bar.end };
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      onNudge?.("move", -1, current);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      onNudge?.("move", 1, current);
    } else if (event.key === "PageUp") {
      event.preventDefault();
      onNudge?.("move", -7, current);
    } else if (event.key === "PageDown") {
      event.preventDefault();
      onNudge?.("move", 7, current);
    }
  }

  function handleResizeHandleKeyDown(edge: "resize-start" | "resize-end") {
    return (event: ReactKeyboardEvent<HTMLDivElement>): void => {
      // A handle is its own independently-focusable widget — its keys must
      // never fall through to the bar body's own move-nudge handling.
      event.stopPropagation();
      if (event.key === "Enter") {
        event.preventDefault();
        onCommit?.();
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        onCancel?.();
        return;
      }
      if (!allowResize || !event.shiftKey) return;
      const current = { start: bar.start, end: bar.end };
      if (event.key === "ArrowLeft") {
        event.preventDefault();
        onNudge?.(edge, -1, current);
      } else if (event.key === "ArrowRight") {
        event.preventDefault();
        onNudge?.(edge, 1, current);
      } else if (event.key === "PageUp") {
        event.preventDefault();
        onNudge?.(edge, -7, current);
      } else if (event.key === "PageDown") {
        event.preventDefault();
        onNudge?.(edge, 7, current);
      }
    };
  }

  function resizeHandle(edge: "resize-start" | "resize-end") {
    return (
      <div
        role="slider"
        tabIndex={0}
        aria-orientation="horizontal"
        aria-valuemin={valueMin}
        aria-valuemax={valueMax}
        aria-valuenow={edge === "resize-start" ? startOffset : endOffset}
        aria-valuetext={formatDate(edge === "resize-start" ? displayStart : displayEnd)}
        aria-label={`Resize ${edge === "resize-start" ? "start" : "end"} of ${bar.label}`}
        onPointerDown={handlePointerDown(edge)}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onKeyDown={handleResizeHandleKeyDown(edge)}
        className="absolute top-0 h-full cursor-ew-resize focus-visible:shadow-focus focus-visible:outline-none"
        style={{
          width: RESIZE_HANDLE_HIT_AREA_PX,
          [edge === "resize-start" ? "left" : "right"]: -((RESIZE_HANDLE_HIT_AREA_PX - 4) / 2),
        }}
      />
    );
  }

  return (
    // biome-ignore lint/a11y/useSemanticElements: role="group" marks a real keyboard-operable move/resize widget, not a form's <fieldset> grouping.
    <div
      role="group"
      // biome-ignore lint/a11y/noNoninteractiveTabindex: this "group" is a real composite widget (pointer-drag, Arrow/PageUp/PageDown/Enter/Escape) and must be a genuine Tab stop.
      tabIndex={0}
      title={bar.label}
      aria-label={accessibleName}
      style={style}
      onPointerDown={handlePointerDown("move")}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onKeyDown={handleBarKeyDown}
      className={`absolute top-0 h-full rounded-control text-sm transition-all duration-(--duration-fast) ease-out hover:shadow-sm focus-visible:shadow-focus focus-visible:outline-none ${colorClassName} ${
        isDragging ? "opacity-85" : ""
      } ${contrast?.needsOutline ? "border border-border" : ""}`}
    >
      <div className="flex h-full items-center truncate px-3 text-left">{showLabel && bar.label}</div>
      {allowResize && (
        <>
          {resizeHandle("resize-start")}
          {resizeHandle("resize-end")}
        </>
      )}
      {isDragging && (
        <div className="-top-6 absolute left-0 whitespace-nowrap rounded-control bg-surface px-1.5 py-0.5 text-text text-xs shadow-md">
          {formatDateRange(displayStart, displayEnd)}
        </div>
      )}
    </div>
  );
}
