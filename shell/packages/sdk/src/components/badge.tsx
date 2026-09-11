import type { ReactNode } from "react";
import { Icon } from "./icon.js";

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
  // Lucide icon name, shown before the label.
  icon?: string | undefined;
}

// docs/components/badge.md "Tokens Used" — gray reuses the existing
// neutral tokens directly; the other 9 are their own dedicated bg/text
// token pairs, calibrated to >=4.5:1 in both light and dark mode.
export const BADGE_COLOR_CLASSES: Record<BadgeColor, string> = {
  gray: "bg-bg-subtle text-text-secondary",
  red: "bg-badge-red text-badge-red-text",
  orange: "bg-badge-orange text-badge-orange-text",
  yellow: "bg-badge-yellow text-badge-yellow-text",
  green: "bg-badge-green text-badge-green-text",
  teal: "bg-badge-teal text-badge-teal-text",
  blue: "bg-badge-blue text-badge-blue-text",
  indigo: "bg-badge-indigo text-badge-indigo-text",
  purple: "bg-badge-purple text-badge-purple-text",
  pink: "bg-badge-pink text-badge-pink-text",
};

export function Badge({ label, color = "gray", icon }: BadgeProps): ReactNode {
  // Callers passing a manifest-sourced value (a loosely-typed wire
  // `string(enum)`, not statically checked) can hand this an unrecognized
  // color — fall back to gray rather than rendering a broken className.
  const colorClasses = BADGE_COLOR_CLASSES[color] ?? BADGE_COLOR_CLASSES.gray;
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${colorClasses}`}>
      {icon && <Icon name={icon} size={12} className="shrink-0" aria-hidden="true" />}
      {label}
    </span>
  );
}
