import type { ReactNode } from "react";
import { formatFieldValue } from "./field.js";
import { Skeleton } from "./skeleton.js";

export type StatCardChangeDirection = "up" | "down";

export interface StatCardChange {
  value: number;
  direction: StatCardChangeDirection;
  period: string;
}

// Always maps to one of shell-visual-design.md's status colors (warning/
// danger) for an at-risk metric — never an arbitrary color.
export type StatCardColor = "red" | "orange";

export interface StatCardProps {
  label: string;
  // `undefined` is a real, expected value — the query hasn't resolved yet.
  value: number | string | undefined;
  change?: StatCardChange | undefined;
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
  color?: StatCardColor | undefined;
  format?: "currency" | undefined;
  currency?: string | undefined;
  href?: string | undefined;
}

const CHANGE_DIRECTION_CLASSES: Record<StatCardChangeDirection, string> = {
  up: "text-green-700",
  down: "text-red-700",
};

const COLOR_CLASSES: Record<StatCardColor, string> = {
  red: "text-red-700",
  orange: "text-orange-700",
};

function formatValue(value: number | string, format: "currency" | undefined, currency: string | undefined): string {
  return format === "currency" ? formatFieldValue(value, "currency", currency, "—") : String(value);
}

export function StatCard({ label, value, change, icon, color, format, currency, href }: StatCardProps): ReactNode {
  const body = (
    <div data-icon={icon} className="rounded-lg border border-border bg-bg p-4">
      <p className="text-fg text-sm">{label}</p>
      {value === undefined ? (
        <Skeleton lines={1} />
      ) : (
        <p className={`font-semibold text-2xl ${color ? COLOR_CLASSES[color] : "text-fg"}`}>
          {formatValue(value, format, currency)}
        </p>
      )}
      {change !== undefined && (
        <p className={`text-xs ${CHANGE_DIRECTION_CLASSES[change.direction]}`}>
          <span aria-hidden="true">{change.direction === "up" ? "▲" : "▼"}</span> {Math.abs(change.value)}{" "}
          {change.period}
        </p>
      )}
    </div>
  );

  return href !== undefined ? <a href={href}>{body}</a> : body;
}
