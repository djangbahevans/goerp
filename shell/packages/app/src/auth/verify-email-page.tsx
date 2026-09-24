import { type EmailVerification, type EmailVerificationOutcome, verifyEmail } from "@goerp/sdk/auth";
import { actionButtonClassName, Countdown, fieldInputClassName, Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, type SubmitEvent, useEffect, useId, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import { ResendStatus, type ResendVerification, useVerificationResend } from "./verification-resend.js";

type Phase =
  | { kind: "idle" }
  | { kind: "verifying" }
  | { kind: "error" }
  | { kind: "expired" }
  | { kind: "locked"; seconds: number; key: number };

// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

// A full page load either way, so the next page re-runs the mount-time
// session check and picks up the session cookies the confirm just set.
function replaceLocation(href: string): void {
  window.location.replace(href);
}

export interface VerifyEmailPageProps {
  // From the verification link's query string; a missing token means the
  // link is unusable.
  token: string | undefined;
  tenant: string | undefined;
  // Storybook and tests substitute these; the route never passes them.
  verify?: (input: EmailVerification) => Promise<EmailVerificationOutcome>;
  resend?: ResendVerification;
  redirect?: (href: string) => void;
}

// shell-ux.md §2.10. Never verifies on load: email scanners open links
// before the recipient does, and would spend the single-use token.
export function VerifyEmailPage({
  token,
  tenant,
  verify = verifyEmail,
  resend,
  redirect = replaceLocation,
}: VerifyEmailPageProps): ReactNode {
  const expiredHeadingRef = useRef<HTMLHeadingElement>(null);
  const [phase, setPhase] = useState<Phase>(token ? { kind: "idle" } : { kind: "expired" });

  // Only a 404 moves here from the form, whose focused button unmounts.
  useEffect(() => {
    if (phase.kind === "expired" && token) expiredHeadingRef.current?.focus();
  }, [phase.kind, token]);

  const verifying = phase.kind === "verifying";
  const locked = phase.kind === "locked";

  const handleVerify = async () => {
    if (verifying || locked || !token) return;
    setPhase({ kind: "verifying" });
    try {
      const outcome = await verify({ token, tenant: tenant ?? "" });
      redirect(outcome === "signed_in" ? "/" : "/auth/login?notice=email_verified");
    } catch (err) {
      if (isAppError(err) && err.httpStatus === 404) {
        setPhase({ kind: "expired" });
      } else if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setPhase({ kind: "locked", seconds, key: Date.now() });
      } else {
        setPhase({ kind: "error" });
      }
    }
  };

  if (phase.kind === "expired") {
    return (
      <AuthLayout>
        <div className="flex flex-col gap-4">
          <h1 ref={expiredHeadingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
            This link has expired
          </h1>
          <p className="text-sm text-text-secondary">
            Verification links expire after 24 hours and work once. If you've already verified, sign in. Otherwise, send
            yourself a new link.
          </p>
          {tenant ? (
            <ExpiredResendForm tenant={tenant} resend={resend} />
          ) : (
            <a href="/auth/login" className={`${actionButtonClassName("primary", "md")} w-full justify-center`}>
              Sign in
            </a>
          )}
        </div>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <div className="flex flex-col gap-4">
        <h1 className="font-semibold text-text text-xl">Verify your email</h1>
        <p className="text-sm text-text-secondary">Confirm your email address to finish setting up your account.</p>

        <button
          type="button"
          onClick={() => void handleVerify()}
          disabled={verifying || locked}
          // Same convention as ActionButton: dimmed when inactive, but full
          // contrast while busy verifying.
          data-disabled={locked ? "true" : undefined}
          aria-busy={verifying}
          className={`${actionButtonClassName("primary", "md")} w-full justify-center`}
        >
          {verifying && <Spinner size={16} />}
          Verify email
        </button>

        {phase.kind === "error" && (
          <p role="alert" className="text-danger text-sm">
            Couldn't verify your email. Try again.
          </p>
        )}
        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {phase.kind === "locked" && (
            <>
              Too many attempts. Try again in{" "}
              <Countdown key={phase.key} seconds={phase.seconds} onComplete={() => setPhase({ kind: "idle" })} />.
            </>
          )}
        </div>
      </div>
    </AuthLayout>
  );
}

function ExpiredResendForm({ tenant, resend }: { tenant: string; resend: ResendVerification | undefined }): ReactNode {
  const emailId = useId();
  const [email, setEmail] = useState("");
  const [emailError, setEmailError] = useState<string | null>(null);
  const resendFlow = useVerificationResend(resend);

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (resendFlow.sending || resendFlow.coolingDown) return;
    if (!email.trim()) {
      setEmailError("Enter your email address.");
      return;
    }
    setEmailError(null);
    await resendFlow.send({ email: email.trim(), tenant });
  };

  const disabled = resendFlow.sending || resendFlow.coolingDown;

  return (
    <>
      <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
        <div className="flex flex-col gap-1">
          <label htmlFor={emailId} className="text-sm text-text">
            Email
          </label>
          <input
            id={emailId}
            type="email"
            autoComplete="email"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            value={email}
            aria-invalid={emailError !== null}
            onChange={(e) => setEmail(e.target.value)}
            className={fieldInputClassName(emailError !== null, "input", "sans")}
          />
          {emailError && (
            <span role="alert" className="text-danger text-sm">
              {emailError}
            </span>
          )}
        </div>

        <ResendStatus resend={resendFlow} />

        <button
          type="submit"
          disabled={disabled}
          data-disabled={resendFlow.coolingDown ? "true" : undefined}
          aria-busy={resendFlow.sending}
          className={`${actionButtonClassName("primary", "md")} w-full justify-center`}
        >
          {resendFlow.sending && <Spinner size={16} />}
          Send a new link
        </button>
      </form>
      <a
        href="/auth/login"
        className="self-center rounded-control text-primary text-sm hover:underline focus-visible:shadow-focus focus-visible:outline-none"
      >
        Sign in
      </a>
    </>
  );
}
