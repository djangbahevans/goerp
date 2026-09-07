import type { ReactNode } from "react";
import { formatFieldValue } from "./field.js";
import { UserAvatar } from "./user-avatar.js";

export interface TimelineItemUser {
  name: string;
  avatarUrl?: string | null | undefined;
}

export interface TimelineItemProps {
  icon?: ReactNode | undefined;
  title: string;
  description?: string | undefined;
  timestamp: Date | string;
  user?: TimelineItemUser | undefined;
}

export interface TimelineProps {
  children: ReactNode;
}

export function Timeline({ children }: TimelineProps): ReactNode {
  return <ol className="flex flex-col">{children}</ol>;
}

// docs/components/timeline.md "Construction: the connecting line" — the
// --space-4 item gap lives on each item's own box (pb-4/last:pb-0), not a
// Timeline-level gap, so the line positioned inside one item can extend
// into the following space. The line spans from the icon circle's bottom
// (top-8, matching the circle's own h-8) to the item's own box bottom, and
// is hidden on the last item since there's nothing left to connect to.
export function TimelineItem({ icon, title, description, timestamp, user }: TimelineItemProps): ReactNode {
  const date = timestamp instanceof Date ? timestamp : new Date(timestamp);
  const isValidDate = !Number.isNaN(date.getTime());

  return (
    <li className="relative flex gap-3 pb-4 last:pb-0 [&:last-child>[data-timeline-line]]:hidden">
      <span
        aria-hidden="true"
        className="relative z-10 flex h-8 w-8 flex-none items-center justify-center rounded-full border border-border bg-surface"
      >
        {icon}
      </span>
      <span data-timeline-line aria-hidden="true" className="absolute bottom-0 left-4 top-8 w-px bg-border" />
      <div className="flex-1">
        <p className="text-sm text-text">{title}</p>
        {description !== undefined && <p className="text-sm text-text-secondary">{description}</p>}
        <div className="flex items-center gap-2 text-text-secondary text-xs">
          <time dateTime={isValidDate ? date.toISOString() : undefined}>
            {formatFieldValue(timestamp, "datetime", undefined, "")}
          </time>
          {user !== undefined && (
            <>
              <UserAvatar name={user.name} avatarUrl={user.avatarUrl} size="xs" showTooltip />
              <span className="text-sm">{user.name}</span>
            </>
          )}
        </div>
      </div>
    </li>
  );
}
