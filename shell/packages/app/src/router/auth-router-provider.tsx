import { isSessionExpired, useAuth, useTenantSuspended } from "@goerp/sdk/auth";
import { type AnyRouter, RouterProvider } from "@tanstack/react-router";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { isAuthPath } from "../auth/safe-redirect.js";

export interface AuthRouterProviderProps {
  router: AnyRouter;
}

// Renders the router with the live auth state as its context. Nothing renders
// until the mount-time session check settles — otherwise the root route's
// auth gate would judge a signed-in user by the pre-check "idle" state and
// bounce them to /auth/login on every reload.
export function AuthRouterProvider({ router }: AuthRouterProviderProps): ReactNode {
  const auth = useAuth();
  const tenantSuspended = useTenantSuspended();
  const [sessionSettled, setSessionSettled] = useState(false);
  const pending = auth.state.status === "idle" || auth.state.status === "checking";
  if (!sessionSettled && !pending) setSessionSettled(true);

  // Kept current even while nothing renders: code outside the router (e.g.
  // ViewRegistryProvider) can invalidate it before RouterProvider mounts.
  router.update({ ...router.options, context: { ...router.options.context, auth } });

  // Re-runs the gate once when the session check first settles, then whenever
  // a session starts or ends (logout, expiry) or the user's MFA-setup
  // requirement flips (a 403 mfa_setup_required, or finishing setup), or a
  // 403 tenant_suspended arrives or is cleared — not on every status change,
  // which would reload route data on each token refresh. The settle re-run is
  // unconditional: a load triggered while pending decided nothing, and
  // RouterProvider skips its own mount-time load when one already resolved or
  // is still in flight — which the public router API can't tell apart from a
  // cold start. On a cold start this runs the initial route checks twice.
  const gated = useRef<string | null>(null);
  // An expiry off the auth pages counts as still signed in here: the page
  // stays put under the session-expired modal rather than reloading its data
  // into 401s. On an auth page (e.g. /auth/mfa-setup) it re-runs the gate,
  // the same as a sign-out.
  const signedIn =
    auth.isAuthenticated || (isSessionExpired(auth.state) && !isAuthPath(router.state.location.pathname));
  const gateKey = `${signedIn}:${auth.user?.mfaSetupRequired === true}:${tenantSuspended}`;
  useEffect(() => {
    if (!sessionSettled || gated.current === gateKey) return;
    gated.current = gateKey;
    void router.invalidate();
  }, [sessionSettled, gateKey, router]);

  if (!sessionSettled) return null;
  return <RouterProvider router={router} context={{ auth }} />;
}
