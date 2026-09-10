import type { CSSProperties, ReactNode } from "react";

export interface ToggleFieldProps {
  id?: string | undefined;
  value: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean | undefined;
}

// Stated pixel width, not derived from the spacing scale — same precedent
// Sidebar.width/NotificationSheet's width already set. Also sidesteps
// goerp#729 (w-* utilities silently generate no CSS in this build) — only
// width needs this; h-5 (the matching height) is confirmed to work.
const TRACK_STYLE: CSSProperties = { width: "36px" };
const THUMB_STYLE: CSSProperties = { width: "var(--space-4)" };
// Track content box: 36px - 2*1px border - 2*2px padding = 30px. Thumb is
// 16px, leaving 14px (translate-x-3.5) of travel — not translate-x-4
// (16px), which would overshoot and lose the 2px inset on the right.
const THUMB_TRAVEL_CLASS = "group-data-[checked=true]:translate-x-3.5";

export function ToggleField({ id, value, onChange, disabled = false }: ToggleFieldProps): ReactNode {
  return (
    <label
      data-checked={value}
      style={TRACK_STYLE}
      className="group inline-flex h-5 shrink-0 cursor-pointer items-center rounded-full border px-0.5 py-0.5 transition-colors duration-(--duration-fast) ease-out has-focus-visible:shadow-focus has-disabled:cursor-not-allowed has-disabled:opacity-50 data-[checked=false]:border-border data-[checked=false]:bg-bg-subtle data-[checked=false]:hover:border-border-strong data-[checked=true]:border-transparent data-[checked=true]:bg-primary data-[checked=true]:hover:bg-primary-hover"
    >
      <input
        id={id}
        type="checkbox"
        role="switch"
        aria-checked={value}
        checked={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="sr-only"
      />
      <span
        aria-hidden
        style={THUMB_STYLE}
        className={`h-4 rounded-full bg-surface shadow-sm transition-transform duration-(--duration-fast) ease-out motion-reduce:transition-none ${THUMB_TRAVEL_CLASS}`}
      />
    </label>
  );
}
