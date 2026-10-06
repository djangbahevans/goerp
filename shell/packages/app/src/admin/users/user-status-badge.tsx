import { Badge, type BadgeColor } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import type { AdminUserStatus } from "./admin-users-api.js";

const STATUS: Record<AdminUserStatus, { label: string; color: BadgeColor }> = {
  active: { label: "Active", color: "green" },
  invited: { label: "Invited", color: "blue" },
  suspended: { label: "Suspended", color: "red" },
  pending_verification: { label: "Unverified", color: "yellow" },
};

// A platform operator's account suspension blocks sign-in everywhere
// whatever the member status says, and no tenant action lifts it.
export function UserStatusBadge({
  status,
  accountSuspended = false,
}: {
  status: AdminUserStatus;
  accountSuspended?: boolean;
}): ReactNode {
  const { label, color } = STATUS[status];
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      <Badge label={label} color={color} />
      {accountSuspended && <Badge label="Suspended by GoERP" color="red" />}
    </span>
  );
}
