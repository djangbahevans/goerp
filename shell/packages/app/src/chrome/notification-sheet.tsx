import { EmptyState, formatRelativeTime, Skeleton } from "@goerp/sdk/components";
import type { Notification } from "@goerp/sdk/notifications";
import { useMarkRead, useNotifications } from "@goerp/sdk/notifications";
import { useNavigate } from "@tanstack/react-router";
import type { ReactNode, UIEvent } from "react";
import { SideSheet } from "./side-sheet.js";

export interface NotificationSheetProps {
  open: boolean;
  onClose: () => void;
}

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

  return (
    <SideSheet open={open} onClose={onClose} title="Notifications" onBodyScroll={handleScroll}>
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
    </SideSheet>
  );
}
