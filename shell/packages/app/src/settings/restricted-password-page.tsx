import { useAuth } from "@goerp/sdk/auth";
import { Button } from "@goerp/sdk/components";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { AuthLayout } from "../auth/auth-layout.js";
import { safeRedirect } from "../auth/safe-redirect.js";
import { ChangePasswordSection } from "./change-password-section.js";

export function RestrictedPasswordPage() {
  const { user, tenant, reloadSession, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const onward = safeRedirect((location.search as { redirect?: unknown }).redirect);
  const [busy, setBusy] = useState(false);
  const [changed, setChanged] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const finish = async () => {
    setChanged(true);
    setBusy(true);
    setError(null);
    try {
      await reloadSession();
      await navigate({ href: onward, replace: true });
    } catch {
      setError("Your password is updated. Couldn't load your session. Try again.");
    } finally {
      setBusy(false);
    }
  };

  const signOut = async () => {
    setBusy(true);
    setError(null);
    try {
      await logout();
      await navigate({ to: "/auth/login", replace: true });
    } catch {
      setError("Couldn't sign out. Try again.");
      setBusy(false);
    }
  };

  return (
    <AuthLayout>
      <h1 className="mb-2 font-semibold text-text text-xl">Change your password to keep using {tenant?.name}</h1>
      <p className="mb-6 text-sm text-text-secondary">It needs at least {user?.passwordMinLength} characters.</p>
      {changed ? (
        <div className="flex flex-col gap-4">
          <p role="status" className="text-sm text-text-secondary">
            Password updated.
          </p>
          {error && (
            <p role="alert" className="text-danger text-sm">
              {error}
            </p>
          )}
          <Button loading={busy} onClick={() => void finish()}>
            Continue
          </Button>
        </div>
      ) : (
        <>
          <ChangePasswordSection restricted onChanged={finish} onBusyChange={setBusy} />
          {error && (
            <p role="alert" className="mt-4 text-danger text-sm">
              {error}
            </p>
          )}
        </>
      )}
      <div className="mt-6 flex justify-center text-sm">
        <Button variant="link" disabled={busy} onClick={() => void signOut()}>
          Sign out
        </Button>
      </div>
    </AuthLayout>
  );
}
