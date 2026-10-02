import {
  isSessionExpired,
  PermissionContext,
  useAuth,
  usePermissionsStatus,
  useTenantSuspended,
} from "@goerp/sdk/auth";
import { useViewRegistryStatus, ViewRegistryContext } from "@goerp/sdk/schema";
import { type AnyRouter, RouterProvider } from "@tanstack/react-router";
import { type ReactNode, useContext, useEffect, useRef, useState } from "react";
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
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  const tenantSuspended = useTenantSuspended();
  const [sessionSettled, setSessionSettled] = useState(false);
  const pending = auth.state.status === "idle" || auth.state.status === "checking";
  if (!sessionSettled && !pending) setSessionSettled(true);

  // Provider effects can invalidate the router before it mounts. Use render-time
  // snapshots so route checks cannot observe data from an earlier provider effect.
  const context = {
    ...router.options.context,
    auth,
    workspace: { registry, permissions, registryStatus, permissionsStatus },
  };
  router.update({ ...router.options, context });

  // A pre-mount invalidation can finish a load before auth settles, so settling
  // must rerun the gate even when RouterProvider skips its mount-time load.
  const gated = useRef<{ key: string; registry: typeof registry; permissions: typeof permissions } | null>(null);
  // An expiry off the auth pages counts as still signed in here: the page
  // stays put under the session-expired modal rather than reloading its data
  // into 401s. On an auth page (e.g. /auth/mfa-setup) it re-runs the gate,
  // the same as a sign-out.
  const signedIn =
    auth.isAuthenticated || (isSessionExpired(auth.state) && !isAuthPath(router.state.location.pathname));
  const gateKey = `${signedIn}:${auth.user?.mfaSetupRequired === true}:${tenantSuspended}:${registryStatus}:${permissionsStatus}`;
  useEffect(() => {
    if (
      !sessionSettled ||
      (gated.current?.key === gateKey &&
        gated.current.registry === registry &&
        gated.current.permissions === permissions)
    )
      return;
    gated.current = { key: gateKey, registry, permissions };
    void router.invalidate();
  }, [sessionSettled, gateKey, registry, permissions, router]);

  if (!sessionSettled) return null;
  return <RouterProvider router={router} context={context} />;
}
