import type { ReactNode } from "react";

export interface ProgressBarProps {
  // 0-100.
  value: number;
  label?: string | undefined;
  showLabel?: boolean | undefined;
}

export function ProgressBar({ value, label, showLabel = false }: ProgressBarProps): ReactNode {
  // Falls back to 0 for NaN (e.g. a caller computing `done / total` before
  // `total` is known) — Math.min/max propagate NaN rather than clamping it.
  const clamped = Number.isNaN(value) ? 0 : Math.min(100, Math.max(0, value));

  return (
    <div>
      {showLabel && label !== undefined && <span className="mb-1 block text-sm text-text-secondary">{label}</span>}
      <div
        role="progressbar"
        aria-valuenow={clamped}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={label}
        className="h-2 overflow-hidden rounded-full bg-border"
      >
        <div
          className="h-full rounded-full bg-primary transition-[width] duration-(--duration-base) ease-out motion-reduce:transition-none"
          style={{ width: `${clamped}%` }}
        />
      </div>
    </div>
  );
}
