import { Toast } from "@goerp/sdk/components";
import { Outlet, useRouterState } from "@tanstack/react-router";
import { isAuthPath } from "../auth/safe-redirect.js";
import { ChromeLayout, CommandPalette } from "../chrome/index.js";

// shell-architecture.md §6: /auth/* pages render bare, everything else inside
// the chrome. Keyed on the resolved location, not the pending one, so the
// frame only switches once <Outlet /> shows the page it belongs to.
export function RootLayout() {
  const pathname = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).pathname });
  return (
    <>
      {isAuthPath(pathname) ? <Outlet /> : <ChromeLayout />}
      <Toast />
      <CommandPalette />
    </>
  );
}
