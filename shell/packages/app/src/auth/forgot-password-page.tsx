import { fetchTenantContext, type PasswordResetRequest, requestPasswordReset } from "@goerp/sdk/auth";
import { actionButtonClassName, Countdown, fieldInputClassName, Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useQuery } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";

type Phase =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "sent" }
  | { kind: "locked"; seconds: number; key: number };

interface FieldErrors {
  email?: string;
  company?: string;
}

// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

const linkClassName =
  "rounded-control text-sm text-primary hover:underline focus-visible:outline-none focus-visible:shadow-focus";

export interface ForgotPasswordPageProps {
  // Storybook substitutes canned responses; the route never passes it.
  requestReset?: (input: PasswordResetRequest) => Promise<void>;
}

// shell-ux.md §2.3. Every successful response shows the same message: the
// engine answers 200 whether or not the email is registered, and nothing
// here may tell the two apart.
export function ForgotPasswordPage({ requestReset = requestPasswordReset }: ForgotPasswordPageProps): ReactNode {
  const tenantContext = useQuery({
    queryKey: ["auth", "tenant-context"],
    queryFn: fetchTenantContext,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });

  const emailId = useId();
  const companyId = useId();
  const sentHeadingRef = useRef<HTMLHeadingElement>(null);

  const [email, setEmail] = useState("");
  const [company, setCompany] = useState("");
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });

  // The form (and the focused submit button) unmounts on success.
  useEffect(() => {
    if (phase.kind === "sent") sentHeadingRef.current?.focus();
  }, [phase.kind]);

  const resolvedTenant = tenantContext.data?.tenant ?? null;
  const showCompanyField = tenantContext.isFetched && resolvedTenant === null;
  const submitting = phase.kind === "submitting";
  const locked = phase.kind === "locked";
  const inputsDisabled = submitting || locked;

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inputsDisabled || !tenantContext.isFetched) return;

    const tenant = resolvedTenant?.slug ?? company.trim();
    const errors: FieldErrors = {};
    if (!email.trim()) errors.email = "Enter your email address.";
    if (showCompanyField && !tenant) errors.company = "Enter your company.";
    setFieldErrors(errors);
    setFormError(null);
    if (Object.keys(errors).length > 0) return;

    setPhase({ kind: "submitting" });
    try {
      await requestReset({ email: email.trim(), tenant });
      setPhase({ kind: "sent" });
    } catch (err) {
      if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setPhase({ kind: "locked", seconds, key: Date.now() });
        return;
      }
      setPhase({ kind: "idle" });
      setFormError(
        isAppError(err)
          ? "Something went wrong. Try again."
          : "Couldn't reach the server. Check your connection and try again.",
      );
    }
  };

  if (phase.kind === "sent") {
    return (
      <AuthLayout>
        <div className="flex flex-col gap-4">
          <h1 ref={sentHeadingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
            Check your email
          </h1>
          <p className="text-sm text-text-secondary">
            If that email is registered, you'll receive a reset link shortly.
          </p>
          <a href="/auth/login" className={`${actionButtonClassName("primary", "md")} w-full justify-center`}>
            Back to sign in
          </a>
        </div>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <div className="mb-6 flex flex-col gap-1">
        <h1 className="font-semibold text-text text-xl">Forgot password</h1>
        <p className="text-sm text-text-secondary">Enter your email and we'll send you a link to reset it.</p>
      </div>

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

        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {formError}
          {phase.kind === "locked" && (
            <>
              Too many requests. Try again in{" "}
              <Countdown key={phase.key} seconds={phase.seconds} onComplete={() => setPhase({ kind: "idle" })} />.
            </>
          )}
        </div>

        <button
          type="submit"
          disabled={inputsDisabled || !tenantContext.isFetched}
          // Same convention as ActionButton: dimmed when inactive, but full
          // contrast while busy submitting.
          data-disabled={locked || !tenantContext.isFetched ? "true" : undefined}
          aria-busy={submitting}
          className={`${actionButtonClassName("primary", "md")} w-full justify-center`}
        >
          {submitting && <Spinner size={16} />}
          Send reset link
        </button>
      </form>

      <p className="mt-6 text-center">
        <a href="/auth/login" className={linkClassName}>
          Back to sign in
        </a>
      </p>
    </AuthLayout>
  );
}
