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

  return (
    <span
      title={title}
      data-user-id={userId}
      className={`inline-flex items-center justify-center rounded-full bg-gray-200 font-medium text-gray-700 ${sizeClass}`}
    >
      {initialsOf(name)}
    </span>
  );
}
