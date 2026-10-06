import {
  Button,
  EmptyState,
  formatRelativeTime,
  Icon,
  isKnownIconName,
  SegmentedField,
  Skeleton,
} from "@goerp/sdk/components";
import type { Notification } from "@goerp/sdk/notifications";
import { useMarkAllRead, useMarkRead, useNotifications, useUnreadCount } from "@goerp/sdk/notifications";
import { useNavigate } from "@tanstack/react-router";
import type { ReactNode, UIEvent } from "react";
import { useEffect, useState } from "react";
import { SideSheet } from "./side-sheet.js";

export interface NotificationSheetProps {
  open: boolean;
  onClose: () => void;
}

// Reaching within this many px of the bottom triggers the next page fetch.
const FETCH_MORE_THRESHOLD_PX = 96;

type FeedFilter = "all" | "unread";

const FILTER_OPTIONS = [
  { value: "all", label: "All" },
  { value: "unread", label: "Unread" },
];

export interface NotificationItemProps {
  notification: Notification;
  onOpen?: ((notification: Notification) => void) | undefined;
}

export function NotificationItem({ notification, onOpen }: NotificationItemProps): ReactNode {
  const unread = notification.readAt === null;
  const rowClasses = `flex gap-3 border-border border-b p-3 text-left ${unread ? "bg-primary-subtle" : ""} ${
    notification.actionUrl ? "cursor-pointer hover:bg-surface-hover" : "cursor-default"
  }`;

  const content = (
    <>
      {unread && <span aria-hidden="true" className="mt-1.5 h-2 w-2 flex-none rounded-full bg-primary" />}
      <div className={`min-w-0 flex-1 ${unread ? "" : "pl-5"}`}>
        <p className={`flex gap-2 text-sm ${unread ? "text-text" : "text-text-secondary"}`}>
          {notification.icon !== null && isKnownIconName(notification.icon) && (
            // The slot is reserved before the glyph's chunk loads, so the title doesn't shift.
            <span className="mt-0.5 size-4 flex-none text-text-secondary">
              <Icon name={notification.icon} size={16} aria-hidden="true" />
            </span>
          )}
          <span className="min-w-0">{notification.title}</span>
        </p>
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
      onClick={() => onOpen?.(notification)}
    >
      {content}
    </button>
  );
}

export function NotificationSheet({ open, onClose }: NotificationSheetProps): ReactNode {
  const [filter, setFilter] = useState<FeedFilter>("all");
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setFilter("all");
  }
  const unreadOnly = filter === "unread";
  const { notifications, isLoading, hasMore, isFetchingNextPage, fetchMore } = useNotifications({
    limit: 20,
    unread: unreadOnly,
  });
  const { count: unreadCount } = useUnreadCount();
  const { mutate: markRead } = useMarkRead();
  const markAllRead = useMarkAllRead();
  const resetMarkAllRead = markAllRead.reset;

  useEffect(() => {
    if (!open) resetMarkAllRead();
  }, [open, resetMarkAllRead]);
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
    <SideSheet
      open={open}
      onClose={onClose}
      title="Notifications"
      onBodyScroll={handleScroll}
      toolbar={
        <div className="border-border border-b px-4 py-2">
          <div className="flex items-center justify-between gap-2">
            <fieldset className="min-w-0">
              <legend className="sr-only">Filter notifications</legend>
              <SegmentedField
                size="sm"
                options={FILTER_OPTIONS}
                value={filter}
                onChange={(value) => setFilter(value === "unread" ? "unread" : "all")}
              />
            </fieldset>
            <Button
              variant="ghost"
              size="sm"
              disabled={unreadCount === 0}
              loading={markAllRead.isPending}
              onClick={() => markAllRead.mutate()}
            >
              Mark all as read
            </Button>
          </div>
          {markAllRead.isError && (
            <p role="alert" className="mt-2 text-danger text-xs">
              Couldn't mark notifications as read.
            </p>
          )}
          <p role="status" className="sr-only">
            {markAllRead.isSuccess ? "All notifications marked as read." : ""}
          </p>
        </div>
      }
    >
      {isLoading ? (
        <div className="p-4">
          <Skeleton lines={5} />
        </div>
      ) : notifications.length === 0 ? (
        <EmptyState title={unreadOnly ? "You're all caught up." : "No notifications yet"} />
      ) : (
        <>
          {notifications.map((notification) => (
            <NotificationItem key={notification.id} notification={notification} onOpen={handleOpen} />
          ))}
          {isFetchingNextPage && (
            <div className="flex justify-center p-3 text-sm text-text-secondary" aria-live="polite">
              Loading more…
            </div>
          )}
        </>
      )}
    </SideSheet>
  );
}
