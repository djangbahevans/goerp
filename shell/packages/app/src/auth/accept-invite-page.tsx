import {
  acceptInvite,
  fetchInviteInfo,
  type InviteAcceptance,
  type InviteAcceptOutcome,
  type InviteInfo,
  type InviteLink,
} from "@goerp/sdk/auth";
import { Button, Countdown, FieldWrapper, Spinner, TextInput } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useQuery } from "@tanstack/react-query";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { ButtonLink } from "../router/button-link.js";
import { AuthLayout } from "./auth-layout.js";
import {
  confirmBlurError,
  type NewPasswordErrors,
  NewPasswordFields,
  validateNewPassword,
} from "./new-password-fields.js";
import { policyMessageAsSentence } from "./password-messages.js";

// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

// A full page load either way, so the next page runs the mount-time session
// check against the cookies the accept response set (or didn't).
function replaceLocation(href: string): void {
  window.location.replace(href);
}

function isDeadLink(err: unknown): boolean {
  return isAppError(err) && err.httpStatus === 404;
}

function acceptErrorMessage(err: unknown): string {
  if (!isAppError(err)) return "Couldn't reach the server. Check your connection and try again.";
  if (err.code === "invite_conflict") return "This account has already been set up. Sign in instead.";
  if (err.code === "overloaded") return "The server is busy. Try again in a moment.";
  return "Something went wrong. Try again.";
}

export interface AcceptInvitePageProps {
  // From the invite link's query string; missing means the link is unusable.
  token: string | undefined;
  tenant: string | undefined;
  // Storybook and tests substitute these; the route never passes them.
  loadInfo?: (link: InviteLink) => Promise<InviteInfo>;
  accept?: (input: InviteAcceptance) => Promise<InviteAcceptOutcome>;
  redirect?: (href: string) => void;
}

// shell-ux.md §2.5.
export function AcceptInvitePage({
  token,
  tenant,
  loadInfo = fetchInviteInfo,
  accept = acceptInvite,
  redirect = replaceLocation,
}: AcceptInvitePageProps): ReactNode {
  const link = token && tenant ? { token, tenant } : null;
  const info = useQuery({
    queryKey: ["auth", "invite-info", token, tenant],
    queryFn: () => loadInfo(link as InviteLink),
    enabled: link !== null,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });

  if (!link || isDeadLink(info.error)) return <DeadLinkCard />;

  if (info.isError) {
    return (
      <AuthLayout>
        <div className="flex flex-col gap-4">
          <h1 className="font-semibold text-text text-xl">Couldn't load your invite</h1>
          <p className="text-sm text-text-secondary">Check your connection and try again.</p>
          <Button variant="primary" fullWidth onClick={() => void info.refetch()}>
            Try again
          </Button>
        </div>
      </AuthLayout>
    );
  }

  if (!info.data) {
    return (
      <AuthLayout>
        <div role="status" className="flex items-center justify-center gap-2 py-8 text-sm text-text-secondary">
          <Spinner size={16} />
          Loading your invite…
        </div>
      </AuthLayout>
    );
  }

  return info.data.passwordRequired ? (
    <NewAccountForm link={link} info={info.data} accept={accept} redirect={redirect} />
  ) : (
    <ExistingAccountAccess link={link} info={info.data} accept={accept} redirect={redirect} />
  );
}

// replacesForm: shown after a submit came back 404, so the focused button is
// gone and focus moves to the heading; on first load focus stays put.
function DeadLinkCard({ replacesForm = false }: { replacesForm?: boolean }): ReactNode {
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (replacesForm) headingRef.current?.focus();
  }, [replacesForm]);
  return (
    <AuthLayout>
      <div className="flex flex-col gap-4">
        <h1 ref={headingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
          Invite expired
        </h1>
        <p className="text-sm text-text-secondary">
          This invite link has expired or has already been used. Ask your administrator to send a new one.
        </p>
        <ButtonLink to="/auth/login" variant="primary" fullWidth>
          Go to sign in
        </ButtonLink>
      </div>
    </AuthLayout>
  );
}

type Phase =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "dead" }
  | { kind: "locked"; seconds: number; key: number };

function NewAccountForm({
  link,
  info,
  accept,
  redirect,
}: {
  link: InviteLink;
  info: InviteInfo;
  accept: (input: InviteAcceptance) => Promise<InviteAcceptOutcome>;
  redirect: (href: string) => void;
}): ReactNode {
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [strongEnough, setStrongEnough] = useState(false);
  const [errors, setErrors] = useState<NewPasswordErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });

  const submitting = phase.kind === "submitting";
  const locked = phase.kind === "locked";
  const inputsDisabled = submitting || locked;

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inputsDisabled) return;

    const found = validateNewPassword(next, confirm, strongEnough);
    setErrors(found);
    setFormError(null);
    if (Object.keys(found).length > 0) return;

    setPhase({ kind: "submitting" });
    try {
      const outcome = await accept({ ...link, password: next });
      redirect(outcome === "signed_in" ? "/" : "/auth/login");
    } catch (err) {
      if (isDeadLink(err)) {
        setPhase({ kind: "dead" });
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
        setFormError(acceptErrorMessage(err));
      }
    }
  };

  if (phase.kind === "dead") return <DeadLinkCard replacesForm />;

  return (
    <AuthLayout>
      <div className="mb-6 flex flex-col gap-1">
        <h1 className="font-semibold text-text text-xl">You've been invited to {info.tenantName}</h1>
        <p className="text-sm text-text-secondary">Set a password to finish creating your account.</p>
      </div>

      <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
        <FieldWrapper label="Email">
          <TextInput
            type="email"
            // Lets password managers file the new password under this account.
            autoComplete="username"
            value={info.email}
            onChange={() => {}}
            readOnly
          />
        </FieldWrapper>

        <NewPasswordFields
          next={next}
          confirm={confirm}
          onNextChange={setNext}
          onConfirmChange={setConfirm}
          onConfirmBlur={() => setErrors((e) => ({ ...e, confirm: confirmBlurError(next, confirm) }))}
          onStrengthChange={setStrongEnough}
          errors={errors}
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

        <Button type="submit" variant="primary" fullWidth disabled={locked} loading={submitting}>
          Set password and sign in
        </Button>
      </form>
    </AuthLayout>
  );
}

type ExistingPhase = { kind: "idle" } | { kind: "accepting" } | { kind: "dead" } | { kind: "failed"; message: string };

// shell-ux.md §2.5 "Existing user path": the invitee keeps their password,
// so there's no form. Accepting never signs an existing account in (the
// link proves control of the mailbox, not of that account's password or
// second factor), so it always continues to sign in.
function ExistingAccountAccess({
  link,
  info,
  accept,
  redirect,
}: {
  link: InviteLink;
  info: InviteInfo;
  accept: (input: InviteAcceptance) => Promise<InviteAcceptOutcome>;
  redirect: (href: string) => void;
}): ReactNode {
  const [phase, setPhase] = useState<ExistingPhase>({ kind: "idle" });
  const accepting = phase.kind === "accepting";

  const handleAccept = async () => {
    if (accepting) return;
    setPhase({ kind: "accepting" });
    try {
      await accept(link);
      redirect("/auth/login");
    } catch (err) {
      setPhase(isDeadLink(err) ? { kind: "dead" } : { kind: "failed", message: acceptErrorMessage(err) });
    }
  };

  if (phase.kind === "dead") return <DeadLinkCard replacesForm />;

  return (
    <AuthLayout>
      <div className="flex flex-col gap-4">
        <h1 className="font-semibold text-text text-xl">You've been invited to {info.tenantName}</h1>
        <p className="text-sm text-text-secondary">
          Accept to add {info.tenantName} to your existing account ({info.email}), then sign in with your existing
          password.
        </p>
        <div role="status" aria-live="polite" className="text-sm text-danger empty:hidden">
          {phase.kind === "failed" && phase.message}
        </div>
        <Button variant="primary" fullWidth loading={accepting} onClick={() => void handleAccept()}>
          Accept and sign in
        </Button>
      </div>
    </AuthLayout>
  );
}
