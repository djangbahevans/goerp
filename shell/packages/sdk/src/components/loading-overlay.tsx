import type { CSSProperties, ReactNode } from "react";

export interface LoadingOverlayProps {
  label?: string | undefined;
}

const overlayStyle: CSSProperties = {
  position: "absolute",
  inset: 0,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  // A visible backdrop, not just floating text — and its own full-box
  // hit area (position: absolute + inset: 0, with no pointer-events:
  // none) already blocks clicks from reaching whatever it covers by
  // default, without needing to say so explicitly.
  background: "rgba(255, 255, 255, 0.75)",
};

// Overlays a parent that establishes its own positioning context (e.g.
// `position: relative`) — see typescript-sdk-reference.md §13's example:
// `<div style={{ position: 'relative' }}><Form /> {isSaving && <LoadingOverlay />}</div>`.
export function LoadingOverlay({ label = "Loading" }: LoadingOverlayProps): ReactNode {
  return (
    <div role="status" aria-busy="true" style={overlayStyle}>
      {label}
    </div>
  );
}
