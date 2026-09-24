import {
  checkSlug as checkSlugAvailability,
  fetchTenantContext,
  type RegisterOutcome,
  type Registration,
  register as registerAccount,
} from "@goerp/sdk/auth";
import { Button, Checkbox, Countdown, FieldWrapper, TextInput, TextLink } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useQuery } from "@tanstack/react-query";
import { Check } from "lucide-react";
import { type ReactNode, type RefObject, type SubmitEvent, useEffect, useRef, useState } from "react";
import { ButtonLink } from "../router/button-link.js";
import { AuthLayout } from "./auth-layout.js";
import { confirmBlurError, NewPasswordFields, validateNewPassword } from "./new-password-fields.js";
import { policyMessageAsSentence } from "./password-messages.js";
import { deriveSlug, isValidSlug } from "./slug.js";
import { ResendStatus, type ResendVerification, useVerificationResend } from "./verification-resend.js";

const SLUG_CHECK_DEBOUNCE_MS = 500;
// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

type Phase =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "locked"; seconds: number; key: number }
  | { kind: "check_email"; email: string; tenantSlug: string }
  | { kind: "provisioning_pending" };

type SlugStatus = "unknown" | "checking" | "available" | "taken";

interface FieldErrors {
  name?: string | undefined;
  email?: string | undefined;
  next?: string | undefined;
  confirm?: string | undefined;
  company?: string | undefined;
  terms?: string | undefined;
}

// A full page load either way, so the next page re-runs the mount-time
// session check and picks up any session cookies registration just set.
function replaceLocation(href: string): void {
  window.location.replace(href);
}

export interface RegisterPageProps {
  // Storybook and tests substitute these; the route never passes them.
  register?: (input: Registration) => Promise<RegisterOutcome>;
  checkSlug?: (slug: string, signal?: AbortSignal) => Promise<boolean>;
  resend?: ResendVerification;
  redirect?: (href: string) => void;
}

// shell-ux.md §2.2.
export function RegisterPage({
  register = registerAccount,
  checkSlug = checkSlugAvailability,
  resend,
  redirect = replaceLocation,
}: RegisterPageProps): ReactNode {
  const tenantContext = useQuery({
    queryKey: ["auth", "tenant-context"],
    queryFn: fetchTenantContext,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });
  const registrationEnabled = tenantContext.data?.registrationEnabled === true;
  const termsUrl = tenantContext.data?.termsUrl ?? null;

  useEffect(() => {
    if (tenantContext.isFetched && !registrationEnabled) redirect("/auth/login");
  }, [tenantContext.isFetched, registrationEnabled, redirect]);

  const cardHeadingRef = useRef<HTMLHeadingElement>(null);

  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [strongEnough, setStrongEnough] = useState(false);
  const [company, setCompany] = useState("");
  const [termsAccepted, setTermsAccepted] = useState(false);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [slugStatus, setSlugStatus] = useState<SlugStatus>("unknown");

  const slug = deriveSlug(company);

  useEffect(() => {
    // Clears the previous name's result until this one's check answers.
    setSlugStatus("unknown");
    if (!isValidSlug(slug)) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setSlugStatus("checking");
      checkSlug(slug, controller.signal).then(
        (available) => {
          if (!controller.signal.aborted) setSlugStatus(available ? "available" : "taken");
        },
        () => {
          if (!controller.signal.aborted) setSlugStatus("unknown");
        },
      );
    }, SLUG_CHECK_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [slug, checkSlug]);

  // The form unmounts, taking its focused button with it.
  useEffect(() => {
    if (phase.kind === "check_email" || phase.kind === "provisioning_pending") cardHeadingRef.current?.focus();
  }, [phase.kind]);

  const submitting = phase.kind === "submitting";
  const locked = phase.kind === "locked";
  const inputsDisabled = submitting || locked;

  const validate = (): FieldErrors => {
    const found: FieldErrors = {};
    if (!name.trim()) found.name = "Enter your name.";
    if (!email.trim()) found.email = "Enter your email address.";
    const password = validateNewPassword(next, confirm, strongEnough, "password");
    if (password.next) found.next = password.next;
    if (password.confirm) found.confirm = password.confirm;
    if (!company.trim()) found.company = "Enter your company name.";
    else if (!isValidSlug(slug)) found.company = "Use at least 3 letters or digits in your company name.";
    if (termsUrl && !termsAccepted) found.terms = "Accept the terms of service to continue.";
    return found;
  };

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inputsDisabled) return;

    const found = validate();
    setErrors(found);
    setFormError(null);
    if (Object.values(found).some(Boolean)) return;

    const submittedEmail = email.trim();
    setPhase({ kind: "submitting" });
    try {
      const outcome = await register({
        name: name.trim(),
        email: submittedEmail,
        password: next,
        companyName: company.trim(),
      });
      switch (outcome.kind) {
        case "signed_in":
          redirect("/");
          return;
        case "login_required":
          redirect("/auth/login");
          return;
        case "verification_required":
          setPhase({ kind: "check_email", email: submittedEmail, tenantSlug: outcome.tenantSlug });
          return;
        case "provisioning_pending":
          setPhase({ kind: "provisioning_pending" });
          return;
      }
    } catch (err) {
      if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setPhase({ kind: "locked", seconds, key: Date.now() });
        return;
      }
      setPhase({ kind: "idle" });
      if (!isAppError(err)) {
        setFormError("Couldn't reach the server. Check your connection and try again.");
      } else if (err.code === "auth.email_already_exists") {
        setErrors({ email: "Email already in use" });
      } else if (err.code === "tenant.slug_taken") {
        setSlugStatus("taken");
        setErrors({ company: "Company name taken, try another" });
      } else if (err.code === "validation_failed") {
        setErrors(fieldErrorsFrom(err.details));
      } else if (err.code === "overloaded") {
        setFormError("The server is busy. Try again in a moment.");
      } else {
        setFormError("Something went wrong. Try again.");
      }
    }
  };

  if (phase.kind === "check_email") {
    return (
      <CheckEmailCard email={phase.email} tenantSlug={phase.tenantSlug} resend={resend} headingRef={cardHeadingRef} />
    );
  }

  if (phase.kind === "provisioning_pending") {
    return (
      <AuthLayout>
        <div className="flex flex-col gap-4">
          <h1 ref={cardHeadingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
            Your workspace is almost ready
          </h1>
          <p className="text-sm text-text-secondary">Sign in in a minute.</p>
          <ButtonLink to="/auth/login" variant="primary" fullWidth>
            Sign in
          </ButtonLink>
        </div>
      </AuthLayout>
    );
  }

  // Nothing renders until the platform confirms registration is on.
  if (!registrationEnabled) return <AuthLayout>{null}</AuthLayout>;

  return (
    <AuthLayout>
      <h1 className="mb-6 font-semibold text-text text-xl">Create your account</h1>

      <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
        <TextField
          label="Full name"
          autoComplete="name"
          value={name}
          onChange={setName}
          error={errors.name}
          disabled={inputsDisabled}
        />
        <TextField
          label="Email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={setEmail}
          error={errors.email}
          disabled={inputsDisabled}
        />
        <NewPasswordFields
          next={next}
          confirm={confirm}
          onNextChange={setNext}
          onConfirmChange={setConfirm}
          onConfirmBlur={() => setErrors((e) => ({ ...e, confirm: confirmBlurError(next, confirm) }))}
          onStrengthChange={setStrongEnough}
          errors={{ next: errors.next, confirm: errors.confirm }}
          disabled={inputsDisabled}
          nextLabel="Password"
          confirmLabel="Confirm password"
        />
        <div className="flex flex-col gap-1">
          <TextField
            label="Company name"
            autoComplete="organization"
            value={company}
            onChange={setCompany}
            error={errors.company}
            disabled={inputsDisabled}
          />
          <div aria-live="polite" className="text-sm empty:hidden">
            {!errors.company && slugStatus === "available" && (
              <span className="flex items-center gap-1 text-success">
                <Check size={16} aria-hidden="true" />
                Available
              </span>
            )}
            {!errors.company && slugStatus === "taken" && <span className="text-danger">Name taken</span>}
          </div>
        </div>

        {termsUrl && (
          <Checkbox
            label={
              <>
                I agree to the{" "}
                <TextLink href={termsUrl} inline external>
                  terms of service
                </TextLink>
              </>
            }
            checked={termsAccepted}
            disabled={inputsDisabled}
            error={errors.terms}
            onChange={setTermsAccepted}
          />
        )}

        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {formError}
          {phase.kind === "locked" && (
            <>
              Too many attempts. Try again in{" "}
              <Countdown key={phase.key} seconds={phase.seconds} onComplete={() => setPhase({ kind: "idle" })} />.
            </>
          )}
        </div>

        <Button type="submit" variant="primary" fullWidth disabled={locked} loading={submitting}>
          Create account
        </Button>
      </form>

      <p className="mt-6 text-center text-sm text-text-secondary">
        Already have an account? <TextLink href="/auth/login">Sign in</TextLink>
      </p>
    </AuthLayout>
  );
}

// Maps a 422's details, keyed by the API's field names, onto the form.
function fieldErrorsFrom(details: Record<string, unknown> | null): FieldErrors {
  const text = (key: string) => (typeof details?.[key] === "string" ? (details[key] as string) : undefined);
  const password = text("password");
  return {
    name: text("name"),
    email: text("email"),
    next: password && policyMessageAsSentence(password),
    company: text("company_name"),
  };
}

interface TextFieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  error: string | undefined;
  disabled: boolean;
  type?: "text" | "email";
  autoComplete: string;
}

function TextField({ label, value, onChange, error, disabled, type = "text", autoComplete }: TextFieldProps) {
  return (
    <FieldWrapper label={label} error={error}>
      <TextInput
        type={type}
        autoComplete={autoComplete}
        {...(type === "email" ? { autoCorrect: "off", autoCapitalize: "none", spellCheck: false } : {})}
        value={value}
        disabled={disabled}
        onChange={onChange}
      />
    </FieldWrapper>
  );
}

function CheckEmailCard({
  email,
  tenantSlug,
  resend,
  headingRef,
}: {
  email: string;
  tenantSlug: string;
  resend: ResendVerification | undefined;
  headingRef: RefObject<HTMLHeadingElement | null>;
}): ReactNode {
  const resendFlow = useVerificationResend(resend);

  return (
    <AuthLayout>
      <div className="flex flex-col gap-4">
        <h1 ref={headingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
          Check your email
        </h1>
        <p className="text-sm text-text-secondary">
          We sent a verification link to <strong className="font-semibold text-text">{email}</strong>. It expires in 24
          hours.
        </p>
        <ResendStatus resend={resendFlow} sentMessage="Sent. Check your inbox and spam folder." />
        <Button
          fullWidth
          disabled={resendFlow.coolingDown}
          loading={resendFlow.sending}
          onClick={() => void resendFlow.send({ email, tenant: tenantSlug })}
        >
          Resend email
        </Button>
        <p className="self-center text-sm">
          <TextLink href="/auth/login">Back to sign in</TextLink>
        </p>
      </div>
    </AuthLayout>
  );
}
