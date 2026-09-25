import { DismissableLayer } from "@radix-ui/react-dismissable-layer";
import type { ReactElement } from "react";

export interface EscapeLayerProps {
  onEscape: () => void;
  // For an overlay with no outside-click handling of its own.
  onPointerDownOutside?: (() => void) | undefined;
  children: ReactElement;
}

// Joins a hand-rolled overlay to the same layer stack Radix's own dialogs and
// popovers use, so one Escape press closes only the topmost open overlay
// (shell-ux.md §7.3). The handled Escape stops there, never reaching an
// ancestor's own keydown handler.
export function EscapeLayer({ onEscape, onPointerDownOutside, children }: EscapeLayerProps): ReactElement {
  return (
    <DismissableLayer
      asChild
      onEscapeKeyDown={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onEscape();
      }}
      onPointerDownOutside={() => onPointerDownOutside?.()}
    >
      {children}
    </DismissableLayer>
  );
}
