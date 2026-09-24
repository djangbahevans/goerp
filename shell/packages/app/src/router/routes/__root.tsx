import type { AuthContextValue } from "@goerp/sdk/auth";
import { createRootRouteWithContext, redirect } from "@tanstack/react-router";
import { isAuthPath } from "../../auth/safe-redirect.js";
import { RootLayout } from "../root-layout.js";

const MFA_SETUP_EXEMPT = new Set(["/auth/mfa-setup", "/auth/logout"]);

export interface RouterContext {
  auth: AuthContextValue;
}

// shell-architecture.md §6 "Auth-gating". Something outside the router (e.g.
// ViewRegistryProvider's invalidate) can trigger a load before the mount-time
// session check settles; that load decides nothing, and AuthRouterProvider
// re-runs the gate once the check does settle.
export const Route = createRootRouteWithContext<RouterContext>()({
  beforeLoad: ({ location, context }) => {
    // shell-ux.md §2.7: a user the tenant requires to enroll in MFA stays on
    // the setup wizard until it's done.
    if (context.auth.user?.mfaSetupRequired && !MFA_SETUP_EXEMPT.has(location.pathname)) {
      // From an auth page (e.g. /auth/login right after signing in), carry
      // its own destination forward rather than the auth page itself.
      const onward = isAuthPath(location.pathname)
        ? (location.search as { redirect?: unknown }).redirect
        : location.href;
      throw redirect({
        to: "/auth/mfa-setup",
        search: typeof onward === "string" ? { redirect: onward } : {},
      });
    }
    if (isAuthPath(location.pathname)) return;
    const { status } = context.auth.state;
    if (status === "idle" || status === "checking" || context.auth.isAuthenticated) return;
    throw redirect({ to: "/auth/login", search: { redirect: location.href } });
  },
  component: RootLayout,
});
