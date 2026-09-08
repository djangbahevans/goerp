import * as DialogPrimitive from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { useBulkAction } from "../react/bulk-action-context.js";

export interface BulkActionPanelProps {
  children: ReactNode;
}

const OVERLAY_CLASSES =
  "fixed inset-0 z-(--z-modal) bg-overlay data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

// Content is the full-viewport flex-centering/focus-trap boundary; the
// visible panel is a plain inner div — same split as AlertDialog's own
// CONTENT_CLASSES, for the same reason (the boundary sizes to the whole
// viewport for centering, independently of the panel's own max-width).
// bulk-action-panel.md's own "Tokens Used" has no motion entry (unlike
// AlertDialog's), so this reuses the shared fade-in/fade-out pair rather
// than inventing bespoke entrance/exit motion.
const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

// Rendered by a module's own bulk_actions "custom" component
// (view-system.md's "Bulk actions") after the shell invokes it — this is
// just the panel chrome; selection state and completion come from the
// `useBulkAction` hook the panel's own children call.
//
// bulk-action-panel.md's Accessibility section: renders as a modal overlay
// (the doc's own resolved presentation), traps focus while shown, and
// Escape runs the same cancellation path a Cancel button does — pulled
// from `useBulkAction()` directly rather than a prop, since the doc rules
// out BulkActionPanel taking any open/onClose/dismissal prop of its own.
// Click-outside is deliberately not wired to cancel: a bulk action holds
// selected-record state a user might lose by an accidental outside click,
// per the same doc section.
export function BulkActionPanel({ children }: BulkActionPanelProps): ReactNode {
  const { onCancel } = useBulkAction();
  // Same workaround AlertDialog uses, for the same reason: Radix's own
  // close-auto-focus restores focus via a triggerRef only a real
  // Trigger populates, and this component never renders one.
  const triggerRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  }, []);

  return (
    <DialogPrimitive.Root open>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-label="Bulk action"
          className={CONTENT_CLASSES}
          onEscapeKeyDown={onCancel}
          onPointerDownOutside={(event) => event.preventDefault()}
          onInteractOutside={(event) => event.preventDefault()}
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
