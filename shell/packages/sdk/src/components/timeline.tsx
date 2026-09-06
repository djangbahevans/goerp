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
  return <ol className="flex flex-col gap-4">{children}</ol>;
}

export function TimelineItem({ icon, title, description, timestamp, user }: TimelineItemProps): ReactNode {
  const date = timestamp instanceof Date ? timestamp : new Date(timestamp);
  const isValidDate = !Number.isNaN(date.getTime());

  return (
    <li className="flex gap-3">
      {icon !== undefined && <span aria-hidden="true">{icon}</span>}
      <div>
        <p className="font-medium text-fg">{title}</p>
        {description !== undefined && <p className="text-fg text-sm">{description}</p>}
        <div className="flex items-center gap-2 text-fg text-xs">
          <time dateTime={isValidDate ? date.toISOString() : undefined}>
            {formatFieldValue(timestamp, "datetime", undefined, "")}
          </time>
          {user !== undefined && (
            <>
              <UserAvatar name={user.name} avatarUrl={user.avatarUrl} size="xs" showTooltip />
              <span>{user.name}</span>
            </>
          )}
        </div>
      </div>
    </li>
  );
}
