import type { ReactNode } from "react";
import type { BadgeColor } from "./badge.js";

export interface StatusDotProps {
  color: BadgeColor;
  label?: string | undefined;
  // Animated overdue/live indicator (Tailwind's animate-pulse).
  pulse?: boolean | undefined;
}

const DOT_COLOR_CLASSES: Record<BadgeColor, string> = {
  gray: "bg-gray-400",
  red: "bg-red-500",
  orange: "bg-orange-500",
  yellow: "bg-yellow-500",
  green: "bg-green-500",
  teal: "bg-teal-500",
  blue: "bg-blue-500",
  indigo: "bg-indigo-500",
  purple: "bg-purple-500",
  pink: "bg-pink-500",
};

export function StatusDot({ color, label, pulse = false }: StatusDotProps): ReactNode {
  // Same defensiveness as Badge — a manifest-sourced color isn't
  // statically checked, so fall back to gray rather than a broken class.
  const colorClasses = DOT_COLOR_CLASSES[color] ?? DOT_COLOR_CLASSES.gray;
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
