import type { CurrentTenant } from "./types.js";
import { useAuth } from "./use-auth.js";

// typescript-sdk-reference.md's useTenant — useUser's own counterpart for
// the current tenant, same "auth-required route" convenience over useAuth().
export function useTenant(): CurrentTenant {
  const { tenant } = useAuth();
  if (!tenant) {
    throw new Error("useTenant must be called within an authenticated route");
  }
  return tenant;
}
