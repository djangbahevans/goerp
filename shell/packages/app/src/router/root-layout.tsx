import { isSessionExpired, useAuth, useTenantSuspended } from "@goerp/sdk/auth";
import { ConfirmDialogHost, Toast } from "@goerp/sdk/components";
import { ModuleNavigationProvider } from "@goerp/sdk/react";
import { useOnline } from "@goerp/sdk/realtime";
import { Outlet, useRouterState } from "@tanstack/react-router";
import { isAuthPath } from "../auth/safe-redirect.js";
import { SessionExpiredGate } from "../auth/session-expired-modal.js";
import { useBundlePreload } from "../bootstrap/use-bundle-preload.js";
import { useModuleHotReload } from "../bootstrap/use-module-hot-reload.js";
import { ChromeLayout, CommandPalette } from "../chrome/index.js";
import { ModalRoot } from "../chrome/modal-root.js";
import { isErrorPath, rendersErrorPage } from "../pages/errors/index.js";
import { RestrictedPasswordPage } from "../settings/restricted-password-page.js";
import { GlobalShortcuts, KeyboardShortcutsDialog } from "../shortcuts/index.js";
import { useModuleNavigate } from "./use-module-navigate.js";

// shell-architecture.md §6: /auth/* pages and error pages render bare,
// everything else inside the chrome. Keyed on the resolved location and
// matches, not the pending ones, so the frame only switches once <Outlet />
// shows the page it belongs to.
export function RootLayout() {
  const online = useOnline();
  const bare = useRouterState({
    select: (s) => {
      const { pathname } = s.resolvedLocation ?? s.location;
      return isAuthPath(pathname) || isErrorPath(pathname) || rendersErrorPage(s.matches, online);
    },
  });
  const navigate = useModuleNavigate();
  const { user, state, isAuthenticated } = useAuth();
  const tenantSuspended = useTenantSuspended();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const restricted =
    user?.passwordChangeRequired &&
    !tenantSuspended &&
    (isAuthenticated || (isSessionExpired(state) && !isAuthPath(pathname)));
  useBundlePreload(isAuthenticated && !restricted);
  useModuleHotReload();
  if (restricted) {
    return (
      <>
        <RestrictedPasswordPage />
        <Toast />
        <SessionExpiredGate />
      </>
    );
  }
  return (
    <ModuleNavigationProvider navigate={navigate}>
      {bare ? <Outlet /> : <ChromeLayout />}
      <ModalRoot />
      <Toast />
      <ConfirmDialogHost />
      <SessionExpiredGate />
      <CommandPalette />
      <KeyboardShortcutsDialog />
      <GlobalShortcuts />
    </ModuleNavigationProvider>
  );
}
