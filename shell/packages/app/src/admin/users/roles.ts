import type { SelectOption } from "@goerp/sdk/components";

// The built-in roles (auth-internals.md §10), which are every role a tenant
// can have until custom roles and GET /admin/roles exist (goerp#1099).
export const ASSIGNABLE_ROLES: SelectOption[] = [
  { value: "admin", label: "Admin" },
  { value: "user", label: "User" },
  { value: "portal", label: "Portal" },
];

export const DEFAULT_INVITE_ROLE = "user";

export function roleLabel(role: string): string {
  return ASSIGNABLE_ROLES.find((option) => option.value === role)?.label ?? role;
}
