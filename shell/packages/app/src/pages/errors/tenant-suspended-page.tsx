import { tenantSuspension, useAuth } from "@goerp/sdk/auth";
import { Button } from "@goerp/sdk/components";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { ErrorLayout } from "./error-layout.js";

export interface TenantSuspendedPageProps {
  // The deployment's VITE_SUPPORT_URL; the "Contact support" action is
  // hidden without one.
  supportUrl?: string | undefined;
}

// shell-ux.md §6.6.
export function TenantSuspendedPage({ supportUrl }: TenantSuspendedPageProps): ReactNode {
  const { logout } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [signingOut, setSigningOut] = useState(false);

  // The flag clears before navigating: while it's set, the root route gate
  // sends every path back here. The login page's cached tenant lookup is
  // dropped so it asks again rather than reusing the rejected one.
  const signInAgain = async () => {
    setSigningOut(true);
    await logout();
    queryClient.removeQueries({ queryKey: ["auth", "tenant-context"] });
    tenantSuspension.set(false);
    await navigate({ to: "/auth/login" });
  };

  return (
    <ErrorLayout
      icon="ban"
      heading="This organisation's account has been suspended"
      description="Please contact support to resolve this."
      actions={
        <>
          {supportUrl && (
            <Button href={supportUrl} variant="primary" target="_blank" rel="noopener noreferrer">
              Contact support
            </Button>
          )}
          <Button onClick={signInAgain} loading={signingOut} variant={supportUrl ? "secondary" : "primary"}>
            Sign in with a different account
          </Button>
        </>
      }
    />
  );
}
