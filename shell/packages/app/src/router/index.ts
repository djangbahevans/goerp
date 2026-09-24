import type { AuthContextValue } from "@goerp/sdk/auth";
import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

// The real auth value arrives through AuthRouterProvider's context prop,
// before any route loads.
export const router = createRouter({ routeTree, context: { auth: undefined as unknown as AuthContextValue } });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
