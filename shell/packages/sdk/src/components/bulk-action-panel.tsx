import * as DialogPrimitive from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { useBulkAction } from "../react/bulk-action-context.js";
import { MODAL_OVERLAY_CLASSES } from "./modal-overlay.js";

export interface BulkActionPanelProps {
  children: ReactNode;
}

// Same full-viewport-boundary/inner-div split as AlertDialog's CONTENT_CLASSES.
const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

// bulk-action-panel.md's Accessibility section: modal overlay, focus trap,
// Escape routed to useBulkAction()'s onCancel(), outside-click suppressed.
export function BulkActionPanel({ children }: BulkActionPanelProps): ReactNode {
  const { onCancel } = useBulkAction();
  // Same triggerRef/onCloseAutoFocus workaround AlertDialog uses, for the same reason (no Trigger is ever rendered).
  const triggerRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  }, []);

  return (
    <DialogPrimitive.Root defaultOpen>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-label="Bulk action"
          className={CONTENT_CLASSES}
          onEscapeKeyDown={onCancel}
          onPointerDownOutside={(event) => event.preventDefault()}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
        >
          <div className="flex items-center gap-3 rounded-structural border border-border bg-surface p-6 shadow-lg">
            {children}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
