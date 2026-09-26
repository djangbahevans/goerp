import { useAuth } from "@goerp/sdk/auth";
import { Spinner } from "@goerp/sdk/components";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useEffect, useRef } from "react";
import { AuthLayout } from "./auth-layout.js";

export interface HandoffPageProps {
  code: string | undefined;
  // Already passed through safeRedirect.
  redirectTo: string;
}

const FAILED = "/auth/login?notice=session_failed";

// shell-ux.md §2.11: the tenant's host exchanges the handoff code a
// shared-domain sign-in carried here, once, then continues as a sign-in
// on this host would.
export function HandoffPage({ code, redirectTo }: HandoffPageProps): ReactNode {
  const { state, completeHandoff } = useAuth();
  const navigate = useNavigate();
  const started = useRef(false);

  useEffect(() => {
    if (state.status === "authenticated" || state.status === "refreshing") {
      void navigate({ href: redirectTo, replace: true });
      return;
    }
    if (state.status === "mfa_required" && started.current) {
      void navigate({ href: `/auth/mfa?${new URLSearchParams({ redirect: redirectTo })}`, replace: true });
      return;
    }
    // completeHandoff needs the mount-time session check to have settled.
    if (state.status !== "unauthenticated" || started.current) return;
    started.current = true;
    if (!code) {
      void navigate({ href: FAILED, replace: true });
      return;
    }
    // Out of the address bar before the request, so the code doesn't stay
    // in this tab's history entry.
    void navigate({ to: "/auth/handoff", search: { redirect: redirectTo }, replace: true });
    completeHandoff(code).catch(() => {
      // The code may already be spent; the user signs in again on this
      // host, where the login page knows the tenant.
      void navigate({ href: FAILED, replace: true });
    });
  }, [state.status, code, redirectTo, completeHandoff, navigate]);

  return (
    <AuthLayout>
      <div role="status" className="flex flex-col items-center gap-3 py-6 text-text-secondary">
        <Spinner size={24} />
        <p>Signing you in…</p>
      </div>
    </AuthLayout>
  );
}
