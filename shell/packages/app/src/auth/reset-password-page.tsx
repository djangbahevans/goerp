import { confirmPasswordReset, type PasswordResetConfirmation, type PasswordResetOutcome } from "@goerp/sdk/auth";
import { actionButtonClassName, Countdown, PasswordField, PasswordStrengthMeter, Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import { PASSWORD_MISMATCH, policyMessageAsSentence } from "./password-messages.js";

type Phase =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "expired" }
  | { kind: "locked"; seconds: number; key: number };

interface FieldErrors {
  next?: string | undefined;
  confirm?: string | undefined;
}

// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

const TOO_WEAK = "Choose a stronger password: at least 12 characters, rated Fair or better.";

// A full page load either way: a confirm revokes every existing session
// (possibly this browser's), so the next page must re-run the mount-time
// session check instead of trusting in-memory auth state.
function replaceLocation(href: string): void {
  window.location.replace(href);
}

export interface ResetPasswordPageProps {
  // From the reset link's query string; missing means the link is unusable.
  token: string | undefined;
  tenant: string | undefined;
  // Storybook and tests substitute these; the route never passes them.
  confirmReset?: (input: PasswordResetConfirmation) => Promise<PasswordResetOutcome>;
  redirect?: (href: string) => void;
}

// shell-ux.md §2.4.
export function ResetPasswordPage({
  token,
  tenant,
  confirmReset = confirmPasswordReset,
  redirect = replaceLocation,
}: ResetPasswordPageProps): ReactNode {
  const expiredHeadingRef = useRef<HTMLHeadingElement>(null);

  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [strongEnough, setStrongEnough] = useState(false);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>(token ? { kind: "idle" } : { kind: "expired" });

  // Only a 404 moves here from the form, whose focused button unmounts.
  useEffect(() => {
    if (phase.kind === "expired" && token) expiredHeadingRef.current?.focus();
  }, [phase.kind, token]);

  const submitting = phase.kind === "submitting";
  const locked = phase.kind === "locked";
  const inputsDisabled = submitting || locked;

  const handleConfirmBlur = () => {
    setErrors((e) => ({ ...e, confirm: confirm && confirm !== next ? PASSWORD_MISMATCH : undefined }));
  };

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inputsDisabled || !token) return;

    const found: FieldErrors = {};
    if (!next) found.next = "Enter a new password.";
    else if (!strongEnough) found.next = TOO_WEAK;
    if (!confirm) found.confirm = "Confirm your new password.";
    else if (confirm !== next) found.confirm = PASSWORD_MISMATCH;
    setErrors(found);
    setFormError(null);
    if (Object.keys(found).length > 0) return;

    setPhase({ kind: "submitting" });
    try {
      const outcome = await confirmReset({ token, newPassword: next, tenant: tenant ?? "" });
      redirect(outcome === "signed_in" ? "/" : "/auth/login");
    } catch (err) {
      if (isAppError(err) && err.httpStatus === 404) {
        setPhase({ kind: "expired" });
        return;
      }
      if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setPhase({ kind: "locked", seconds, key: Date.now() });
        return;
      }
      setPhase({ kind: "idle" });
      if (isAppError(err) && err.code === "auth.password_too_weak") {
        setErrors({ next: policyMessageAsSentence(err.message) });
      } else {
        setFormError(
          isAppError(err)
            ? "Something went wrong. Try again."
            : "Couldn't reach the server. Check your connection and try again.",
        );
      }
    }
  };

  if (phase.kind === "expired") {
    return (
      <AuthLayout>
        <div className="flex flex-col gap-4">
          <h1 ref={expiredHeadingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
            Link expired
          </h1>
          <p className="text-sm text-text-secondary">
            This reset link has expired or has already been used. Request a new one.
          </p>
          <a href="/auth/forgot-password" className={`${actionButtonClassName("primary", "md")} w-full justify-center`}>
            Request a new link
          </a>
        </div>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 font-semibold text-text text-xl">Set a new password</h1>

      <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
        <div className="flex flex-col gap-2">
          <PasswordField
            label="New password"
            autoComplete="new-password"
            value={next}
            onChange={setNext}
            error={errors.next}
            disabled={inputsDisabled}
          />
          <PasswordStrengthMeter password={next} onValidityChange={setStrongEnough} />
        </div>
        <PasswordField
          label="Confirm new password"
          autoComplete="new-password"
          value={confirm}
          onChange={setConfirm}
          onBlur={handleConfirmBlur}
          error={errors.confirm}
          disabled={inputsDisabled}
        />

        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {formError}
          {phase.kind === "locked" && (
            <>
              Too many attempts. Try again in{" "}
              <Countdown key={phase.key} seconds={phase.seconds} onComplete={() => setPhase({ kind: "idle" })} />.
            </>
          )}
        </div>

        <button
          type="submit"
          disabled={inputsDisabled}
          // Same convention as ActionButton: dimmed when inactive, but full
          // contrast while busy submitting.
          data-disabled={locked ? "true" : undefined}
          aria-busy={submitting}
          className={`${actionButtonClassName("primary", "md")} w-full justify-center`}
        >
          {submitting && <Spinner size={16} />}
          Set new password
        </button>
      </form>
    </AuthLayout>
  );
}
