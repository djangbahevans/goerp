import type { ReactNode } from "react";
import { useState } from "react";

export type UserAvatarSize = "xs" | "sm" | "md" | "lg";

export interface UserAvatarProps {
  userId?: string | undefined;
  name: string;
  avatarUrl?: string | null | undefined;
  size?: UserAvatarSize | undefined;
  showTooltip?: boolean | undefined;
}

const SIZE_CLASSES: Record<UserAvatarSize, string> = {
  xs: "h-5 w-5 text-[10px]",
  sm: "h-6 w-6 text-xs",
  md: "h-8 w-8 text-xs",
  lg: "h-10 w-10 text-sm",
};

function initialsOf(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("");
}

const AVATAR_BG_CLASSES = [
  "bg-avatar-0",
  "bg-avatar-1",
  "bg-avatar-2",
  "bg-avatar-3",
  "bg-avatar-4",
  "bg-avatar-5",
  "bg-avatar-6",
  "bg-avatar-7",
];

function avatarBgClassFor(key: string): string {
  let hash = 0;
  for (let i = 0; i < key.length; i++) {
    hash = (hash * 31 + key.charCodeAt(i)) | 0;
  }
  return AVATAR_BG_CLASSES[Math.abs(hash) % AVATAR_BG_CLASSES.length] as string;
}

export function UserAvatar({ userId, name, avatarUrl, size = "md", showTooltip = false }: UserAvatarProps): ReactNode {
  const sizeClass = SIZE_CLASSES[size];
  const title = showTooltip ? name : undefined;
  // Keyed by URL so a refreshed signed URL gets a fresh load attempt.
  const [failedUrl, setFailedUrl] = useState<string | null>(null);

  if (avatarUrl && avatarUrl !== failedUrl) {
    return (
      <img
        src={avatarUrl}
        loading="lazy"
        alt={name}
        title={title}
        data-user-id={userId}
        onError={() => setFailedUrl(avatarUrl)}
        className={`rounded-full object-cover ${sizeClass}`}
      />
    );
  }

  // userId is preferred over name because it stays stable when a display name changes.
  const bgClass = avatarBgClassFor(userId || name);

  return (
    <span
      role="img"
      aria-label={name}
      title={title}
      data-user-id={userId}
      className={`inline-flex items-center justify-center rounded-full font-medium text-avatar-text ${bgClass} ${sizeClass}`}
    >
      {initialsOf(name)}
    </span>
  );
}
