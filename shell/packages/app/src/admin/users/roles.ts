import type { SelectOption } from "@goerp/sdk/components";
import { useMemo } from "react";
import { useAdminRoles } from "../roles/admin-roles-api.js";

// The built-in roles (auth-internals.md §10), which every tenant has.
const BUILT_IN_ROLES: SelectOption[] = [
  { value: "admin", label: "Admin" },
  { value: "user", label: "User" },
  { value: "portal", label: "Portal" },
];

export const DEFAULT_INVITE_ROLE = "user";

export function roleLabel(role: string): string {
  return BUILT_IN_ROLES.find((option) => option.value === role)?.label ?? role;
}

// The tenant's roles from GET /admin/roles, or the built-in roles until it
// answers.
export function useAssignableRoles(): SelectOption[] {
  const { data } = useAdminRoles();
  return useMemo(
    () => (data ? data.map((role) => ({ value: role.name, label: roleLabel(role.name) })) : BUILT_IN_ROLES),
    [data],
  );
}
