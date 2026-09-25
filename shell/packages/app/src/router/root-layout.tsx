import { ConfirmDialogHost, Toast } from "@goerp/sdk/components";
import { ModuleNavigationProvider } from "@goerp/sdk/react";
import { Outlet, useRouterState } from "@tanstack/react-router";
import { isAuthPath } from "../auth/safe-redirect.js";
import { SessionExpiredGate } from "../auth/session-expired-modal.js";
import { ChromeLayout, CommandPalette } from "../chrome/index.js";
import { isErrorPath, rendersErrorPage } from "../pages/errors/index.js";
import { GlobalShortcuts, KeyboardShortcutsDialog } from "../shortcuts/index.js";
import { useModuleNavigate } from "./use-module-navigate.js";

// shell-architecture.md §6: /auth/* pages and error pages render bare,
// everything else inside the chrome. Keyed on the resolved location and
// matches, not the pending ones, so the frame only switches once <Outlet />
// shows the page it belongs to.
export function RootLayout() {
  const bare = useRouterState({
    select: (s) => {
      const { pathname } = s.resolvedLocation ?? s.location;
      return isAuthPath(pathname) || isErrorPath(pathname) || rendersErrorPage(s.matches);
    },
  });
  const navigate = useModuleNavigate();
  return (
    <ModuleNavigationProvider navigate={navigate}>
      {bare ? <Outlet /> : <ChromeLayout />}
      <Toast />
      <ConfirmDialogHost />
      <SessionExpiredGate />
      <CommandPalette />
      <KeyboardShortcutsDialog />
      <GlobalShortcuts />
    </ModuleNavigationProvider>
  );
}
