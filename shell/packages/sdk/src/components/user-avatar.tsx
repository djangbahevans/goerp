import type { ReactNode } from "react";

export type UserAvatarSize = "xs" | "sm" | "md" | "lg";

export interface UserAvatarProps {
  userId?: string | undefined;
  name: string;
  avatarUrl?: string | null | undefined;
  size?: UserAvatarSize | undefined;
  showTooltip?: boolean | undefined;
}

// column-renderers.tsx's own "avatar" list column type is a fixed size
// (h-8 w-8, "md" here) with no tooltip — this is a superset, not a
// divergent implementation.
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

// docs/components/user-avatar.md "Tokens Used" — a small string hash of
// userId ?? name, mod 8, into the fixed --color-avatar-0..7 palette.
// userId is preferred when present since it's stable even if a display
// name changes.
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

  if (avatarUrl) {
    return (
      <img
        src={avatarUrl}
        alt={name}
        title={title}
        data-user-id={userId}
        className={`rounded-full object-cover ${sizeClass}`}
      />
    );
  }

  const bgClass = avatarBgClassFor(userId || name);

  return (
    <span
      title={title}
      data-user-id={userId}
      className={`inline-flex items-center justify-center rounded-full font-medium text-avatar-text ${bgClass} ${sizeClass}`}
    >
      {initialsOf(name)}
    </span>
  );
}
