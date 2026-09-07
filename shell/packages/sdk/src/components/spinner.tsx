import type { ReactNode } from "react";

export interface SpinnerProps {
  size: number;
}

// Shared between ActionButton's loading state, LoadingOverlay, and Toast's
// loading variant — a currentColor SVG so each caller sets its color via
// its own text color, sized in pixels since callers need different fixed
// sizes (ActionButton's sm/md button heights, LoadingOverlay's 24px).
export function Spinner({ size }: SpinnerProps): ReactNode {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      className="animate-spin motion-reduce:animate-none"
      style={{ animationDuration: "var(--duration-slower)" }}
    >
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" strokeOpacity="0.25" />
      <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}
