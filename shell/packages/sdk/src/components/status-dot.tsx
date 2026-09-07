import type { ReactNode } from "react";

// Maps to the four functional status tokens (--color-success/-danger/
// -warning/-info), not Badge's full 10-value decorative BadgeColor palette
// — StatusDot signals status/liveness specifically, never a decorative
// choice like Badge's 'teal'/'indigo'/'pink'.
export type StatusDotColor = "green" | "red" | "orange" | "blue";

export interface StatusDotProps {
  color: StatusDotColor;
  label?: string | undefined;
  // Animated overdue/live indicator (Tailwind's animate-pulse).
  pulse?: boolean | undefined;
}

const DOT_COLOR_CLASSES: Record<StatusDotColor, string> = {
  green: "bg-green-500",
  red: "bg-red-500",
  orange: "bg-orange-500",
  blue: "bg-blue-500",
};

export function StatusDot({ color, label, pulse = false }: StatusDotProps): ReactNode {
  // Same defensiveness as Badge — a manifest-sourced or otherwise
  // untyped-at-the-boundary color isn't statically checked, so fall back
  // to a neutral gray rather than a broken class.
  const colorClasses = DOT_COLOR_CLASSES[color] ?? "bg-gray-400";
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        role="img"
        aria-label={label ?? color}
        className={`inline-block h-2 w-2 rounded-full ${colorClasses} ${pulse ? "animate-pulse" : ""}`}
      />
      {label !== undefined && <span>{label}</span>}
    </span>
  );
}
