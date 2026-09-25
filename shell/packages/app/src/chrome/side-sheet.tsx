import { EscapeLayer, IconButton } from "@goerp/sdk/components";
import type { ReactNode, UIEventHandler } from "react";
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

export interface SideSheetProps {
  open: boolean;
  onClose: () => void;
  title: string;
  onBodyScroll?: UIEventHandler<HTMLDivElement> | undefined;
  children: ReactNode;
}

// Hand-rolled, not @radix-ui/react-dialog: Dialog.Content's FocusScope
// hardcodes loop:true regardless of `modal`, so Tab never escapes the
// panel even with modal={false} (confirmed empirically) — this panel owns
// its own portal and focus handling instead.
// Matches --duration-fast — same timer-based exit-animation pattern as
// Toast (toast.tsx), which also has no Radix Presence to lean on.
const EXIT_DURATION_MS = 150;

// LTR only, with physical (not logical start/end) utilities throughout —
// matches the rest of this codebase's own convention (e.g. data-table.tsx's
// text-left). No RTL support pending real dir-aware infra.
const OVERLAY_CLASSES = "fixed inset-0 z-(--z-modal) bg-overlay";
const OVERLAY_ENTER_CLASSES = "animate-[fade-in_var(--duration-slow)_ease-out]";
const OVERLAY_EXIT_CLASSES = "animate-[fade-out_var(--duration-fast)_ease-in]";

const CONTENT_CLASSES =
  "fixed top-0 right-0 bottom-0 z-(--z-modal) flex w-[min(400px,100vw)] flex-col rounded-l-structural bg-surface shadow-lg focus:outline-none";
const CONTENT_ENTER_CLASSES =
  "animate-[side-sheet-content-show_var(--duration-base)_ease-out] motion-reduce:animate-[fade-in_var(--duration-base)_ease-out]";
const CONTENT_EXIT_CLASSES =
  "animate-[side-sheet-content-hide_var(--duration-fast)_ease-in] motion-reduce:animate-[fade-out_var(--duration-fast)_ease-in]";

// notification-sheet.md's slide-over shell, shared by NotificationSheet and
// HelpPanel: backdrop, edge-anchored panel, heading focus on open, focus
// back to the trigger on close, and Escape through the overlay layer stack.
export function SideSheet({ open, onClose, title, onBodyScroll, children }: SideSheetProps): ReactNode {
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Captured on open and restored on close, since the sheet's trigger is
  // rendered by its owner — this panel never receives it as a prop.
  const triggerRef = useRef<HTMLElement | null>(null);
  // Stays true through the close animation — see EXIT_DURATION_MS above.
  const [rendered, setRendered] = useState(open);
  // Starts closed, so a sheet mounted open still records its trigger.
  const prevOpenRef = useRef(false);

  useEffect(() => {
    if (open === prevOpenRef.current) return;
    prevOpenRef.current = open;
    if (open) {
      triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setRendered(true);
      return;
    }
    triggerRef.current?.focus();
    const timer = setTimeout(() => setRendered(false), EXIT_DURATION_MS);
    return () => clearTimeout(timer);
  }, [open]);

  // Separate effect: on open, headingRef only exists once `rendered` has
  // itself committed true, one render after the effect above sets it.
  useEffect(() => {
    if (open && rendered) headingRef.current?.focus();
  }, [open, rendered]);

  if (!rendered) return null;

  return createPortal(
    <>
      <div
        aria-hidden="true"
        className={`${OVERLAY_CLASSES} ${open ? OVERLAY_ENTER_CLASSES : OVERLAY_EXIT_CLASSES}`}
        onClick={onClose}
      />
      <EscapeLayer onEscape={onClose}>
        <div
          role="dialog"
          aria-labelledby={headingId}
          className={`${CONTENT_CLASSES} ${open ? CONTENT_ENTER_CLASSES : CONTENT_EXIT_CLASSES}`}
        >
          <div className="flex items-center justify-between border-border border-b p-4">
            <h2
              id={headingId}
              ref={headingRef}
              tabIndex={-1}
              className="font-semibold text-lg text-text focus:outline-none"
            >
              {title}
            </h2>
            <IconButton icon="x" label="Close" size="sm" onClick={onClose} />
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto" onScroll={onBodyScroll}>
            {children}
          </div>
        </div>
      </EscapeLayer>
    </>,
    document.body,
  );
}
