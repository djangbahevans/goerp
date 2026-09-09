import { EmptyState, formatRelativeTime, Skeleton } from "@goerp/sdk/components";
import type { Notification } from "@goerp/sdk/notifications";
import { useMarkRead, useNotifications } from "@goerp/sdk/notifications";
import { useNavigate } from "@tanstack/react-router";
import { X } from "lucide-react";
import type { CSSProperties, ReactNode, UIEvent } from "react";
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

export interface NotificationSheetProps {
  open: boolean;
  onClose: () => void;
}

// Hand-rolled, not @radix-ui/react-dialog: Dialog.Content's FocusScope
// hardcodes loop:true regardless of `modal`, so Tab never escapes the
// panel even with modal={false} (confirmed empirically) — this panel owns
// its own portal, focus, and Escape handling instead.
// Matches --duration-fast — same timer-based exit-animation pattern as
// Toast (toast.tsx), which also has no Radix Presence to lean on.
const EXIT_DURATION_MS = 150;

// LTR only, with physical (not logical start/end) utilities throughout —
// matches the rest of this codebase's own convention (e.g. data-table.tsx's
// text-left). No RTL support pending real dir-aware infra.
const OVERLAY_CLASSES = "fixed inset-0 z-(--z-modal) bg-overlay";
const OVERLAY_ENTER_CLASSES = "animate-[fade-in_var(--duration-slow)_ease-out]";
const OVERLAY_EXIT_CLASSES = "animate-[fade-out_var(--duration-fast)_ease-in]";

// Individual top/right/left offsets and single-corner rounded-* utilities
// confirmed (empirically, via inspecting the compiled CSS) to generate no
// rule at all in this project's @tailwindcss/vite setup — both the named
// scale and arbitrary-bracket forms — while inset-0/bottom-0 and the
// uniform rounded-structural do. Inline style sidesteps it; every other
// declaration here still goes through Tailwind classes as normal.
const CONTENT_CLASSES =
  "fixed bottom-0 z-(--z-modal) flex w-[min(400px,100vw)] flex-col bg-surface shadow-lg focus:outline-none";
const CONTENT_STYLE: CSSProperties = {
  top: 0,
  right: 0,
  borderTopLeftRadius: "var(--radius-structural)",
  borderBottomLeftRadius: "var(--radius-structural)",
};
const CONTENT_ENTER_CLASSES =
  "animate-[notification-sheet-content-show_var(--duration-base)_ease-out] motion-reduce:animate-[fade-in_var(--duration-base)_ease-out]";
const CONTENT_EXIT_CLASSES =
  "animate-[notification-sheet-content-hide_var(--duration-fast)_ease-in] motion-reduce:animate-[fade-out_var(--duration-fast)_ease-in]";

// Reaching within this many px of the bottom triggers the next page fetch.
const FETCH_MORE_THRESHOLD_PX = 96;

interface NotificationItemProps {
  notification: Notification;
  onOpen: (notification: Notification) => void;
}

function NotificationItem({ notification, onOpen }: NotificationItemProps): ReactNode {
  const unread = notification.readAt === null;
  const rowClasses = `flex gap-3 border-border border-b p-3 text-left ${unread ? "bg-primary-subtle" : ""} ${
    notification.actionUrl ? "cursor-pointer hover:bg-surface-hover" : "cursor-default"
  }`;

  const content = (
    <>
      {unread && <span aria-hidden="true" className="mt-1.5 h-2 w-2 flex-none rounded-full bg-primary" />}
      <div className={`min-w-0 flex-1 ${unread ? "" : "pl-5"}`}>
        <p className={`text-sm ${unread ? "text-text" : "text-text-secondary"}`}>{notification.title}</p>
        {notification.body !== null && <p className="mt-1 text-sm text-text-secondary">{notification.body}</p>}
        <p className="mt-2 text-text-secondary text-xs">{formatRelativeTime(notification.createdAt, "")}</p>
      </div>
    </>
  );

  if (!notification.actionUrl) {
    return (
      <div className={rowClasses} data-notification-id={notification.id}>
        {content}
      </div>
    );
  }

  return (
    <button
      type="button"
      className={`w-full ${rowClasses}`}
      data-notification-id={notification.id}
      onClick={() => onOpen(notification)}
    >
      {content}
    </button>
  );
}

export function NotificationSheet({ open, onClose }: NotificationSheetProps): ReactNode {
  const { notifications, isLoading, hasMore, isFetchingNextPage, fetchMore } = useNotifications({ limit: 20 });
  const { mutate: markRead } = useMarkRead();
  const navigate = useNavigate();
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Captured on open and restored on close, since NotificationBell renders
  // its own trigger — this panel never receives it as a prop.
  const triggerRef = useRef<HTMLElement | null>(null);
  // Stays true through the close animation — see EXIT_DURATION_MS above.
  const [rendered, setRendered] = useState(open);
  const prevOpenRef = useRef(open);

  useEffect(() => {
    if (open === prevOpenRef.current) return;
    prevOpenRef.current = open;
    if (open) {
      triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setRendered(true);
      return;
    }
    const timer = setTimeout(() => setRendered(false), EXIT_DURATION_MS);
    return () => clearTimeout(timer);
  }, [open]);

  // Separate effect: on open, headingRef only exists once `rendered` has
  // itself committed true, one render after the effect above sets it.
  useEffect(() => {
    if (open && rendered) headingRef.current?.focus();
    if (!open) triggerRef.current?.focus();
  }, [open, rendered]);

  useEffect(() => {
    if (!open) return;
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") onClose();
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, onClose]);

  function handleOpen(notification: Notification): void {
    markRead(notification.id);
    if (notification.actionUrl) void navigate({ to: notification.actionUrl });
    onClose();
  }

  function handleScroll(event: UIEvent<HTMLDivElement>): void {
    if (!hasMore || isFetchingNextPage) return;
    const el = event.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < FETCH_MORE_THRESHOLD_PX) fetchMore();
  }

  if (!rendered) return null;

  return createPortal(
    <>
      <div
        aria-hidden="true"
        className={`${OVERLAY_CLASSES} ${open ? OVERLAY_ENTER_CLASSES : OVERLAY_EXIT_CLASSES}`}
        onClick={onClose}
      />
      <div
        role="dialog"
        aria-labelledby={headingId}
        style={CONTENT_STYLE}
        className={`${CONTENT_CLASSES} ${open ? CONTENT_ENTER_CLASSES : CONTENT_EXIT_CLASSES}`}
      >
        <div className="flex items-center justify-between border-border border-b p-4">
          <h2
            id={headingId}
            ref={headingRef}
            tabIndex={-1}
            className="font-semibold text-lg text-text focus:outline-none"
          >
            Notifications
          </h2>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="rounded-control p-1 text-text-secondary hover:bg-surface-hover"
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto" onScroll={handleScroll}>
          {isLoading ? (
            <div className="p-4">
              <Skeleton lines={5} />
            </div>
          ) : notifications.length === 0 ? (
            <EmptyState title="No notifications yet" />
          ) : (
            <>
              {notifications.map((notification) => (
                <NotificationItem key={notification.id} notification={notification} onOpen={handleOpen} />
              ))}
              {isFetchingNextPage && (
                <div className="flex justify-center p-3 text-text-secondary text-sm" aria-live="polite">
                  Loading more…
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </>,
    document.body,
  );
}
