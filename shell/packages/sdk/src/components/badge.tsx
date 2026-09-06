import type { ReactNode } from "react";

// typescript-sdk-reference.md §13 / manifest-spec.md's BadgeConfig.color
// (same 10 values, both places).
export type BadgeColor =
  | "gray"
  | "red"
  | "orange"
  | "yellow"
  | "green"
  | "teal"
  | "blue"
  | "indigo"
  | "purple"
  | "pink";

export interface BadgeProps {
  label: string;
  color?: BadgeColor | undefined;
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
}

// Matches column-renderers.tsx's Pill/BADGE_COLOR_CLASSES exactly, so a
// custom view's Badge looks identical to the manifest-driven "badge" list
// column type it also renders from.
export const BADGE_COLOR_CLASSES: Record<BadgeColor, string> = {
  gray: "bg-gray-100 text-gray-800",
  red: "bg-red-100 text-red-800",
  orange: "bg-orange-100 text-orange-800",
  yellow: "bg-yellow-100 text-yellow-800",
  green: "bg-green-100 text-green-800",
  teal: "bg-teal-100 text-teal-800",
  blue: "bg-blue-100 text-blue-800",
  indigo: "bg-indigo-100 text-indigo-800",
  purple: "bg-purple-100 text-purple-800",
  pink: "bg-pink-100 text-pink-800",
};

export function Badge({ label, color = "gray", icon }: BadgeProps): ReactNode {
  // Callers passing a manifest-sourced value (a loosely-typed wire
  // `string(enum)`, not statically checked) can hand this an unrecognized
  // color — fall back to gray rather than rendering a broken className.
  const colorClasses = BADGE_COLOR_CLASSES[color] ?? BADGE_COLOR_CLASSES.gray;
  return (
    <span
      data-icon={icon}
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colorClasses}`}
    >
      {label}
    </span>
  );
}
