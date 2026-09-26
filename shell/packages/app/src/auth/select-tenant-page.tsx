import { type TenantSelection, useAuth } from "@goerp/sdk/auth";
import { Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useNavigate } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import { handoffURL } from "./handoff-url.js";
import { pendingTenantSelection } from "./tenant-selection.js";

export interface SelectTenantPageProps {
  // Already passed through safeRedirect.
  redirectTo: string;
  // Storybook and tests substitute these; the route never passes them.
  selection?: TenantSelection | null | undefined;
  leave?: ((href: string) => void) | undefined;
}

const START_OVER = "/auth/login?notice=session_failed";

function assignLocation(href: string): void {
  window.location.assign(href);
}

// shell-ux.md §2.9: a tenantless sign-in whose account belongs to several
// tenants picks one here, against the selection_token the login answered
// with, without typing the password again.
export function SelectTenantPage({
  redirectTo,
  selection: given,
  leave = assignLocation,
}: SelectTenantPageProps): ReactNode {
  // Read once: the pick clears the pending selection, and the page must
  // not then mistake itself for one reached without a sign-in.
  const [selection] = useState(() => (given === undefined ? pendingTenantSelection.get() : given));
  const { state, selectTenant } = useAuth();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [choosing, setChoosing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const submitted = useRef(false);

  useEffect(() => {
    if (!selection) void navigate({ href: "/auth/login", replace: true });
    else headingRef.current?.focus();
  }, [selection, navigate]);

  useEffect(() => {
    if (!submitted.current) return;
    if (state.status === "authenticated" || state.status === "refreshing") {
      void navigate({ href: redirectTo, replace: true });
    } else if (state.status === "mfa_required") {
      void navigate({ href: `/auth/mfa?${new URLSearchParams({ redirect: redirectTo })}`, replace: true });
    }
  }, [state.status, navigate, redirectTo]);

  if (!selection) return <AuthLayout>{null}</AuthLayout>;

  const choose = async (slug: string) => {
    if (choosing) return;
    setChoosing(slug);
    setError(null);
    submitted.current = true;
    try {
      const handoff = await selectTenant(selection.selectionToken, slug);
      pendingTenantSelection.set(null);
      if (handoff) leave(handoffURL(handoff, redirectTo));
    } catch (err) {
      submitted.current = false;
      setChoosing(null);
      if (!isAppError(err)) {
        setError("Couldn't reach the server. Check your connection and try again.");
        return;
      }
      // Spent, expired, or no longer a member: the pick can't be retried.
      pendingTenantSelection.set(null);
      void navigate({ href: START_OVER, replace: true });
    }
  };

  return (
    <AuthLayout>
      <div className="mb-6 flex flex-col gap-1">
        <h1 ref={headingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
          Choose an organisation
        </h1>
        <p className="text-sm text-text-secondary">Your account belongs to more than one.</p>
      </div>
      <ul className="flex flex-col gap-2">
        {selection.tenants.map((tenant) => (
          <li key={tenant.slug}>
            <button
              type="button"
              disabled={choosing !== null}
              aria-busy={choosing === tenant.slug}
              onClick={() => void choose(tenant.slug)}
              className="flex w-full items-center gap-3 rounded-control border border-border bg-surface px-4 py-3 text-left hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60"
            >
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium text-sm text-text">{tenant.name}</span>
                {tenant.slug !== tenant.name && (
                  <span className="truncate text-text-secondary text-xs">{tenant.slug}</span>
                )}
              </span>
              {choosing === tenant.slug ? (
                <Spinner size={16} />
              ) : (
                <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-text-secondary" />
              )}
            </button>
          </li>
        ))}
      </ul>
      {error && (
        <p role="alert" className="mt-4 text-danger text-sm">
          {error}
        </p>
      )}
    </AuthLayout>
  );
}
