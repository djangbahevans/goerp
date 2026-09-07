import type { ReactNode } from "react";
import { Spinner } from "./spinner.js";

export interface LoadingOverlayProps {
  label?: string | undefined;
}

// Overlays a parent that establishes its own positioning context (e.g.
// `position: relative`) — see typescript-sdk-reference.md §13's example:
// `<div style={{ position: 'relative' }}><Form /> {isSaving && <LoadingOverlay />}</div>`.
//
// Renders as a sibling of the content it covers, not a wrapper around it,
// so it has no way to disable that content itself — the covered element's
// own wrapper needs the native `inert` attribute paired alongside this
// (`<div className="relative" inert={isSaving}><Form /></div>`), or a
// keyboard/screen-reader user can still reach and submit fields that are
// only visually obscured.
export function LoadingOverlay({ label = "Loading" }: LoadingOverlayProps): ReactNode {
  return (
    <div
      role="status"
      aria-busy="true"
      className="absolute inset-0 z-(--z-raised) flex flex-col items-center justify-center gap-2 bg-[color-mix(in_srgb,var(--color-surface)_85%,transparent)]"
    >
      <span className="text-primary">
        <Spinner size={24} />
      </span>
      <span className="text-sm text-text-secondary">{label}</span>
    </div>
  );
}
