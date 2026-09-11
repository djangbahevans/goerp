import type { ReactNode } from "react";

// Maps to the four functional status tokens (--color-success/-danger/
// -warning/-info) plus a neutral "off"/false state, not Badge's full
// 10-value decorative BadgeColor palette — StatusDot signals
// status/liveness specifically, never a decorative choice like Badge's
// 'teal'/'indigo'/'pink'.
export type StatusDotColor = "green" | "red" | "orange" | "blue" | "gray";

export interface StatusDotProps {
  color: StatusDotColor;
  label?: string | undefined;
  // Animated overdue/live indicator (Tailwind's animate-pulse).
  pulse?: boolean | undefined;
}

// docs/components/status-dot.md "Tokens Used" — the four functional status
// tokens directly, unlike Badge's separate decorative palette, plus
// --color-text-secondary for the neutral "gray" state (list-renderer.md's
// `"boolean"` column type: a dot signaling "false" isn't itself a status,
// so it takes the same neutral token the rest of the library uses for
// secondary/inactive text rather than one of the four functional colors).
const DOT_COLOR_CLASSES: Record<StatusDotColor, string> = {
  green: "bg-success",
  red: "bg-danger",
  orange: "bg-warning",
  blue: "bg-info",
  gray: "bg-text-secondary",
};

export function StatusDot({ color, label, pulse = false }: StatusDotProps): ReactNode {
  // Same defensiveness as Badge — a manifest-sourced or otherwise
  // untyped-at-the-boundary color isn't statically checked, so fall back
  // to a neutral gray rather than a broken class.
  const colorClasses = DOT_COLOR_CLASSES[color] ?? "bg-text-disabled";
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        role="img"
        aria-label={label ?? color}
        className={`inline-block h-2 w-2 rounded-full ${colorClasses} ${pulse ? "animate-pulse motion-reduce:animate-none" : ""}`}
        // Overrides just animate-pulse's fixed 2s duration, same pattern as ActionButton's loading spinner.
        style={{ animationDuration: pulse ? "var(--duration-slow)" : undefined }}
      />
      {label !== undefined && <span>{label}</span>}
    </span>
  );
}
