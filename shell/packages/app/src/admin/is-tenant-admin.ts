import type { CurrentUser } from "@goerp/sdk/auth";

// shell-ux.md §5: the tenant admin section is gated on the built-in `admin` role.
export function isTenantAdmin(user: CurrentUser | null | undefined): boolean {
  return user?.roles.includes("admin") === true;
}
