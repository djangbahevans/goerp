import { isSessionExpired, useAuth, useTenantSuspended } from "@goerp/sdk/auth";
import { Button, Icon, MODAL_OVERLAY_CLASSES } from "@goerp/sdk/components";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { focusManager } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { type ReactNode, useEffect } from "react";
import { isAuthPath } from "./safe-redirect.js";

export interface SessionExpiredModalProps {
  onSignIn: () => void;
}

const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out]";

// shell-ux.md §6.4: over the page the session expired on, which stays
// visible but blurred and inert. `open` is fixed and there's no onOpenChange,
// so Escape and outside clicks can't close it: signing in again is the only
// way out.
export function SessionExpiredModal({ onSignIn }: SessionExpiredModalProps): ReactNode {
  return (
    <DialogPrimitive.Root open>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={`${MODAL_OVERLAY_CLASSES} backdrop-blur-sm`} />
        <DialogPrimitive.Content role="alertdialog" className={CONTENT_CLASSES}>
          <div className="w-full max-w-100 rounded-structural bg-surface p-6 shadow-lg">
            <div className="flex items-center gap-3">
              <Icon name="lock" size={20} className="shrink-0 text-text-secondary" aria-hidden="true" />
              <DialogPrimitive.Title className="font-semibold text-lg text-text">
                Your session has expired
              </DialogPrimitive.Title>
            </div>
            <DialogPrimitive.Description className="mt-3 text-base text-text-secondary">
              Please sign in again to continue.
            </DialogPrimitive.Description>
            <div className="mt-6 flex justify-end">
              <Button variant="primary" onClick={onSignIn}>
                Sign in again
              </Button>
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

// Shows the modal on any page but the auth pages while the session is
// expired; "Sign in again" comes back to the page it was shown over. A
// suspended tenant's page owns that flow instead (shell-ux.md §6.6).
export function SessionExpiredGate(): ReactNode {
  const { state } = useAuth();
  const tenantSuspended = useTenantSuspended();
  const navigate = useNavigate();
  const location = useRouterState({ select: (s) => s.resolvedLocation ?? s.location });
  const showing = isSessionExpired(state) && !isAuthPath(location.pathname) && !tenantSuspended;

  // Refetch-on-focus and interval polling would keep the page firing
  // requests into 401s behind the modal; an unfocused window pauses both.
  useEffect(() => {
    if (!showing) return;
    focusManager.setFocused(false);
    return () => focusManager.setFocused(undefined);
  }, [showing]);

  if (!showing) return null;
  return (
    <SessionExpiredModal onSignIn={() => void navigate({ to: "/auth/login", search: { redirect: location.href } })} />
  );
}
