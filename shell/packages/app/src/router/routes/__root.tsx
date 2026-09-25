import { type AuthContextValue, isSessionExpired, tenantSuspension } from "@goerp/sdk/auth";
import { createRootRouteWithContext, redirect } from "@tanstack/react-router";
import { isAuthPath } from "../../auth/safe-redirect.js";
import { NotFoundPage } from "../../pages/errors/index.js";
import { RootLayout } from "../root-layout.js";

const TENANT_SUSPENDED_PATH = "/tenant-suspended";
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
    // shell-ux.md §6.6: a suspended tenant has nothing else to show, signed
    // in or not, so its page is exempt from the sign-in gate below.
    if (location.pathname === TENANT_SUSPENDED_PATH) return;
    if (tenantSuspension.get()) throw redirect({ to: TENANT_SUSPENDED_PATH });
    // shell-ux.md §2.7: a user the tenant requires to enroll in MFA stays on
    // the setup wizard until it's done.
    if (
      context.auth.isAuthenticated &&
      context.auth.user?.mfaSetupRequired &&
      !MFA_SETUP_EXEMPT.has(location.pathname)
    ) {
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
    // shell-ux.md §6.4: an expired session keeps its page under the
    // session-expired modal, which does the redirect to sign in.
    if (isSessionExpired(context.auth.state)) return;
    throw redirect({ to: "/auth/login", search: { redirect: location.href } });
  },
  component: RootLayout,
  notFoundComponent: NotFoundPage,
});
