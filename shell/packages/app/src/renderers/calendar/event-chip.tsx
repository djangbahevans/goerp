import { useTheme } from "@goerp/sdk/react";
import type { CSSProperties, KeyboardEvent, ReactNode, Ref } from "react";
import { formatEventTime } from "./calendar-date-utils.js";
import type { CalendarEvent } from "./calendar-view-types.js";
import { contrastFor } from "./contrast.js";

export interface EventChipProps {
  event: CalendarEvent;
  onClick?: ((event: CalendarEvent) => void) | undefined;
  // A day cell/time slot nests its EventChips as individually-reachable,
  // individually-focusable buttons (view-system.md's month-grid AC) rather
  // than ordinary Tab stops — plain "button" semantics, not an ARIA
  // "gridcell" role override (an interactive element shouldn't carry a
  // non-interactive containment role).
  tabIndex?: number | undefined;
  onKeyDown?: ((event: KeyboardEvent<HTMLButtonElement>) => void) | undefined;
  ref?: Ref<HTMLButtonElement> | undefined;
}

export function EventChip({ event, onClick, tabIndex = 0, onKeyDown, ref }: EventChipProps): ReactNode {
  const { theme } = useTheme();
  const contrast = event.color ? contrastFor(event.color, theme) : null;

  const style: CSSProperties = {
    ...(event.color ? { backgroundColor: event.color } : {}),
    ...(contrast?.text.kind === "literal" ? { color: contrast.text.hex } : {}),
  };
  const textClassName = contrast
    ? contrast.text.kind === "token"
      ? contrast.text.className
      : ""
    : "bg-bg-subtle text-text";
  const accessibleName = `${event.title}, ${formatEventTime(event.start, event.end)}`;

  return (
    <button
      ref={ref}
      type="button"
      tabIndex={tabIndex}
      onClick={() => onClick?.(event)}
      onKeyDown={onKeyDown}
      title={event.title}
      aria-label={accessibleName}
      style={style}
      className={`w-full truncate rounded-control px-1.5 py-0.5 text-left text-xs transition-shadow duration-(--duration-fast) ease-out hover:shadow-sm focus-visible:shadow-focus ${textClassName} ${
        contrast?.needsOutline ? "border border-border" : ""
      }`}
    >
      {event.title}
    </button>
  );
}
