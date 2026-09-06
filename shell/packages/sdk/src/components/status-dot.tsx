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
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        role="img"
        aria-label={label ?? color}
        className={`inline-block h-2 w-2 rounded-full ${DOT_COLOR_CLASSES[color]} ${pulse ? "animate-pulse" : ""}`}
      />
      {label !== undefined && <span>{label}</span>}
    </span>
  );
}
