import type { ReactNode } from "react";

export interface ProgressBarProps {
  // 0-100.
  value: number;
  label?: string | undefined;
  showLabel?: boolean | undefined;
  // "danger" is file-field.md's resolution of this component's own open
  // question: a failed upload switches the fill to --color-danger rather
  // than the caller replacing ProgressBar with a separate error treatment.
  status?: "default" | "danger" | undefined;
}

export function ProgressBar({ value, label, showLabel = false, status = "default" }: ProgressBarProps): ReactNode {
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
          className={`h-full rounded-full transition-[width] duration-(--duration-base) ease-out motion-reduce:transition-none ${
            status === "danger" ? "bg-danger" : "bg-primary"
          }`}
          style={{ width: `${clamped}%` }}
        />
      </div>
    </div>
  );
}
