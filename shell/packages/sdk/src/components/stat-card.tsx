import type { ReactNode } from "react";

export type StatCardChangeDirection = "up" | "down";

export interface StatCardChange {
  value: number;
  direction: StatCardChangeDirection;
  period: string;
}

export interface StatCardProps {
  label: string;
  value: string | number;
  change?: StatCardChange | undefined;
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
  href?: string | undefined;
}

const CHANGE_DIRECTION_CLASSES: Record<StatCardChangeDirection, string> = {
  up: "text-green-700",
  down: "text-red-700",
};

export function StatCard({ label, value, change, icon, href }: StatCardProps): ReactNode {
  const body = (
    <div data-icon={icon} className="rounded-lg border border-border bg-bg p-4">
      <p className="text-fg text-sm">{label}</p>
      <p className="font-semibold text-fg text-2xl">{value}</p>
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
