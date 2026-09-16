import type { CurrentUser } from "./types.js";
import { useAuth } from "./use-auth.js";

// typescript-sdk-reference.md's useUser — a convenience over useAuth() for
// a route already known to require auth, where a nullable user would just
// mean handling a case that can't actually happen there.
export function useUser(): CurrentUser {
  const { user } = useAuth();
  if (!user) {
    throw new Error("useUser must be called within an authenticated route");
  }
  return user;
}
