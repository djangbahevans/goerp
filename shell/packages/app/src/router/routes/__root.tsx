import type { AuthContextValue } from "@goerp/sdk/auth";
import { createRootRouteWithContext, redirect } from "@tanstack/react-router";
import { isAuthPath } from "../../auth/safe-redirect.js";
import { RootLayout } from "../root-layout.js";

export interface RouterContext {
  auth: AuthContextValue;
}

// shell-architecture.md §6 "Auth-gating". Something outside the router (e.g.
// ViewRegistryProvider's invalidate) can trigger a load before the mount-time
// session check settles; that load decides nothing, and AuthRouterProvider
// re-runs the gate once the check does settle.
export const Route = createRootRouteWithContext<RouterContext>()({
  beforeLoad: ({ location, context }) => {
    if (isAuthPath(location.pathname)) return;
    const { status } = context.auth.state;
    if (status === "idle" || status === "checking" || context.auth.isAuthenticated) return;
    throw redirect({ to: "/auth/login", search: { redirect: location.href } });
  },
  component: RootLayout,
});
