import { fetchTenantContext, useAuth, type VerificationEmailRequest } from "@goerp/sdk/auth";
import { Button, Countdown, fieldInputClassName, PasswordField } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useEffect, useId, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import { ResendStatus, type ResendVerification, useVerificationResend } from "./verification-resend.js";

// Why the user was sent here — set by the MFA challenge page when a
// rejected attempt has spent its single-use mfa_token, or by the
// verify-email page once the email is verified.
const LOGIN_NOTICES = {
  mfa_failed: "Incorrect or expired code. Sign in again.",
  mfa_locked: "Too many failed verification attempts. Try again later.",
  session_failed: "Couldn't finish signing you in. Sign in again.",
  email_verified: "Email verified. Sign in to continue.",
} as const;

export type LoginNotice = keyof typeof LOGIN_NOTICES;

export function isLoginNotice(value: unknown): value is LoginNotice {
  return typeof value === "string" && Object.hasOwn(LOGIN_NOTICES, value);
}

export interface LoginPageProps {
  // Already passed through safeRedirect.
  redirectTo: string;
  notice?: LoginNotice | undefined;
  // Storybook substitutes this; the route never passes it.
  resendVerification?: ResendVerification | undefined;
}

type Phase = { kind: "idle" } | { kind: "submitting" } | { kind: "locked"; seconds: number; key: number };

interface FieldErrors {
  email?: string;
  password?: string;
  company?: string;
}

// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

const linkClassName =
  "rounded-control text-sm text-primary hover:underline focus-visible:outline-none focus-visible:shadow-focus";

function withRedirect(path: string, redirectTo: string): string {
  return `${path}?${new URLSearchParams({ redirect: redirectTo })}`;
}

export function LoginPage({ redirectTo, notice, resendVerification }: LoginPageProps): ReactNode {
  const { state, login } = useAuth();
  const navigate = useNavigate();
  const tenantContext = useQuery({
    queryKey: ["auth", "tenant-context"],
    queryFn: fetchTenantContext,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });

  const emailId = useId();
  const companyId = useId();
  const rememberId = useId();
  const emailRef = useRef<HTMLInputElement>(null);

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [company, setCompany] = useState("");
  const [remember, setRemember] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [noticeDismissed, setNoticeDismissed] = useState(false);
  // The email and tenant of the sign-in that needs verifying, which the
  // resend action uses even if the fields have changed since.
  const [unverified, setUnverified] = useState<VerificationEmailRequest | null>(null);
  const resend = useVerificationResend(resendVerification);
  // Bumped to request email focus once the post-failure render has
  // re-enabled the input (a disabled input can't take focus).
  const [emailFocusRequest, setEmailFocusRequest] = useState(0);

  useEffect(() => {
    if (emailFocusRequest > 0) emailRef.current?.focus();
  }, [emailFocusRequest]);

  const isAuthenticated = state.status === "authenticated" || state.status === "refreshing";
  useEffect(() => {
    if (isAuthenticated) void navigate({ href: redirectTo, replace: true });
  }, [isAuthenticated, navigate, redirectTo]);

  // Only a login this page submitted hands off to the MFA challenge — not an
  // mfa_required state left over from an earlier, abandoned attempt.
  const loginSubmitted = useRef(false);
  useEffect(() => {
    if (state.status === "mfa_required" && loginSubmitted.current) {
      loginSubmitted.current = false;
      void navigate({ href: withRedirect("/auth/mfa", redirectTo) });
    }
  }, [state.status, navigate, redirectTo]);

  const resolvedTenant = tenantContext.data?.tenant ?? null;
  const showCompanyField = tenantContext.isFetched && resolvedTenant === null;
  const submitting = phase.kind === "submitting";
  const locked = phase.kind === "locked";
  // login() rejects outright while the mount-time session check (or a
  // logout) still owns the auth machine.
  const sessionCheckPending =
    !submitting && (state.status === "idle" || state.status === "checking" || state.status === "logging_out");
  const inputsDisabled = submitting || locked;

  const failCredentials = (message: string) => {
    setPassword("");
    setFormError(message);
    setEmailFocusRequest((n) => n + 1);
  };

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inputsDisabled || sessionCheckPending || !tenantContext.isFetched) return;

    const tenant = resolvedTenant?.slug ?? company.trim();
    const errors: FieldErrors = {};
    if (!email.trim()) errors.email = "Enter your email address.";
    if (!password) errors.password = "Enter your password.";
    if (showCompanyField && !tenant) errors.company = "Enter your company.";
    setFieldErrors(errors);
    setFormError(null);
    setUnverified(null);
    setNoticeDismissed(true);
    if (Object.keys(errors).length > 0) return;

    setPhase({ kind: "submitting" });
    loginSubmitted.current = true;
    try {
      // Success re-renders as authenticated or mfa_required, and the effects
      // above navigate from there.
      await login({ email: email.trim(), password, tenant, remember });
      setPhase({ kind: "idle" });
    } catch (err) {
      loginSubmitted.current = false;
      if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setPassword("");
        setPhase({ kind: "locked", seconds, key: Date.now() });
        return;
      }
      setPhase({ kind: "idle" });
      if (!isAppError(err)) {
        setFormError("Couldn't reach the server. Check your connection and try again.");
      } else if (err.code === "email_verification_required") {
        setFormError("Verify your email address before signing in. Check your inbox for the verification link.");
        setUnverified({ email: email.trim(), tenant });
      } else if (err.isUnauth()) {
        failCredentials("Invalid email or password");
      } else {
        setFormError("Something went wrong. Try again.");
      }
    }
  };

  return (
    <AuthLayout>
      <div className="mb-6 flex flex-col gap-1">
        <h1 className="font-semibold text-text text-xl">Sign in</h1>
        {resolvedTenant && <p className="text-sm text-text-secondary">{resolvedTenant.name}</p>}
      </div>

      {notice &&
        !noticeDismissed &&
        (notice === "email_verified" ? (
          <p role="status" className="mb-4 text-sm text-success">
            {LOGIN_NOTICES[notice]}
          </p>
        ) : (
          <p role="alert" className="mb-4 text-danger text-sm">
            {LOGIN_NOTICES[notice]}
          </p>
        ))}

      <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
        {showCompanyField && (
          <div className="flex flex-col gap-1">
            <label htmlFor={companyId} className="text-sm text-text">
              Company
            </label>
            <input
              id={companyId}
              type="text"
              autoComplete="organization"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              value={company}
              disabled={inputsDisabled}
              aria-invalid={fieldErrors.company !== undefined}
              onChange={(e) => setCompany(e.target.value)}
              className={fieldInputClassName(fieldErrors.company !== undefined, "input", "sans")}
            />
            {fieldErrors.company && (
              <span role="alert" className="text-danger text-sm">
                {fieldErrors.company}
              </span>
            )}
          </div>
        )}

        <div className="flex flex-col gap-1">
          <label htmlFor={emailId} className="text-sm text-text">
            Email
          </label>
          <input
            ref={emailRef}
            id={emailId}
            type="email"
            autoComplete="email"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            value={email}
            disabled={inputsDisabled}
            aria-invalid={fieldErrors.email !== undefined}
            onChange={(e) => setEmail(e.target.value)}
            className={fieldInputClassName(fieldErrors.email !== undefined, "input", "sans")}
          />
          {fieldErrors.email && (
            <span role="alert" className="text-danger text-sm">
              {fieldErrors.email}
            </span>
          )}
        </div>

        <PasswordField
          label="Password"
          value={password}
          onChange={setPassword}
          autoComplete="current-password"
          disabled={inputsDisabled}
          error={fieldErrors.password ?? formError ?? undefined}
        />

        {unverified && (
          <div className="flex flex-col items-start gap-1">
            {!resend.coolingDown && (
              <button
                type="button"
                disabled={resend.sending}
                aria-busy={resend.sending}
                onClick={() => void resend.send(unverified)}
                className={linkClassName}
              >
                Resend verification email
              </button>
            )}
            <ResendStatus resend={resend} />
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
          <label htmlFor={rememberId} className="flex items-center gap-2 whitespace-nowrap text-sm text-text">
            <input
              id={rememberId}
              type="checkbox"
              checked={remember}
              disabled={inputsDisabled}
              onChange={(e) => setRemember(e.target.checked)}
              className="accent-primary"
            />
            Remember this device
          </label>
          <a href="/auth/forgot-password" className={`${linkClassName} whitespace-nowrap`}>
            Forgot password?
          </a>
        </div>

        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {phase.kind === "locked" && (
            <>
              Too many attempts. Try again in{" "}
              <Countdown key={phase.key} seconds={phase.seconds} onComplete={() => setPhase({ kind: "idle" })} />.
            </>
          )}
        </div>

        <Button
          type="submit"
          variant="primary"
          fullWidth
          disabled={locked || sessionCheckPending || !tenantContext.isFetched}
          loading={submitting}
        >
          Sign in
        </Button>
      </form>

      {tenantContext.data?.registrationEnabled && (
        <p className="mt-6 text-center text-sm text-text-secondary">
          Don't have an account?{" "}
          <a href="/auth/register" className={linkClassName}>
            Create an account
          </a>
        </p>
      )}
    </AuthLayout>
  );
}
