import { useAuth } from "@goerp/sdk/auth";
import { type AnyRouter, RouterProvider } from "@tanstack/react-router";
import { type ReactNode, useEffect, useRef, useState } from "react";

export interface AuthRouterProviderProps {
  router: AnyRouter;
}

// Renders the router with the live auth state as its context. Nothing renders
// until the mount-time session check settles — otherwise the root route's
// auth gate would judge a signed-in user by the pre-check "idle" state and
// bounce them to /auth/login on every reload.
export function AuthRouterProvider({ router }: AuthRouterProviderProps): ReactNode {
  const auth = useAuth();
  const [sessionSettled, setSessionSettled] = useState(false);
  const pending = auth.state.status === "idle" || auth.state.status === "checking";
  if (!sessionSettled && !pending) setSessionSettled(true);

  // Kept current even while nothing renders: code outside the router (e.g.
  // ViewRegistryProvider) can invalidate it before RouterProvider mounts.
  router.update({ ...router.options, context: { ...router.options.context, auth } });

  // Re-runs the gate once when the session check first settles, then whenever
  // a session starts or ends (logout, expiry) — not on every status change,
  // which would reload route data on each token refresh. The settle re-run is
  // unconditional: a load triggered while pending decided nothing, and
  // RouterProvider skips its own mount-time load when one already resolved or
  // is still in flight — which the public router API can't tell apart from a
  // cold start. On a cold start this runs the initial route checks twice.
  const gatedAuthenticated = useRef<boolean | null>(null);
  useEffect(() => {
    if (!sessionSettled || gatedAuthenticated.current === auth.isAuthenticated) return;
    gatedAuthenticated.current = auth.isAuthenticated;
    void router.invalidate();
  }, [sessionSettled, auth.isAuthenticated, router]);

  if (!sessionSettled) return null;
  return <RouterProvider router={router} context={{ auth }} />;
}
