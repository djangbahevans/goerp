import { Badge, type BadgeColor } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import type { AdminUserStatus } from "./admin-users-api.js";

const STATUS: Record<AdminUserStatus, { label: string; color: BadgeColor }> = {
  active: { label: "Active", color: "green" },
  invited: { label: "Invited", color: "blue" },
  suspended: { label: "Suspended", color: "red" },
  pending_verification: { label: "Unverified", color: "yellow" },
};

export function UserStatusBadge({ status }: { status: AdminUserStatus }): ReactNode {
  const { label, color } = STATUS[status];
  return <Badge label={label} color={color} />;
}
