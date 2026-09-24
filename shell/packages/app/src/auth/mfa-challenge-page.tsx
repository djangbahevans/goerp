import { type MFAMethod, useAuth } from "@goerp/sdk/auth";
import { actionButtonClassName, fieldInputClassName, Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useEffect, useId, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import type { LoginNotice } from "./login-page.js";
import { VerificationCodeInput } from "./verification-code-input.js";

export interface MFAChallengePageProps {
  // Already passed through safeRedirect.
  redirectTo: string;
}

type Mode = "totp" | "recovery_code";

const TOTP_LENGTH = 6;
const RECOVERY_CODE_PATTERN = /^[A-Z2-7]{10}$/;

// Recovery codes are hashed exactly as generated — uppercase base32 as
// XXXXX-XXXXX — so typed input is normalized to that form before sending.
// Returns null for anything that can't be a recovery code, which is never
// sent: every submitted attempt spends the single-use mfa_token.
export function normalizeRecoveryCode(raw: string): string | null {
  const symbols = raw.toUpperCase().replace(/[^A-Z0-9]/g, "");
  if (!RECOVERY_CODE_PATTERN.test(symbols)) return null;
  return `${symbols.slice(0, 5)}-${symbols.slice(5)}`;
}

function loginHref(redirectTo: string, notice?: LoginNotice): string {
  const params = new URLSearchParams({ redirect: redirectTo });
  if (notice) params.set("notice", notice);
  return `/auth/login?${params}`;
}

const linkButtonClassName =
  "self-center rounded-control text-sm text-primary hover:underline focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50";

export function MFAChallengePage({ redirectTo }: MFAChallengePageProps): ReactNode {
  const { state, submitMFA } = useAuth();
  const navigate = useNavigate();
  const recoveryId = useId();
  const codeRef = useRef<HTMLInputElement>(null);
  const recoveryRef = useRef<HTMLInputElement>(null);

  const methods: MFAMethod[] = state.status === "mfa_required" ? state.methods : [];
  const canTOTP = methods.includes("totp");
  const canRecovery = methods.includes("recovery_code");

  const [mode, setMode] = useState<Mode>(canTOTP ? "totp" : "recovery_code");
  const [code, setCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [verifying, setVerifying] = useState(false);
  // Where a rejected attempt sends the user once the machine has left
  // mfa_required; set before verifying clears so the redirect below sees it.
  const failureNotice = useRef<LoginNotice | undefined>(undefined);
  const [focusRequest, setFocusRequest] = useState(0);

  // Success lands in authenticated; a rejected attempt (or a visit with no
  // pending challenge) lands in unauthenticated. Held off while a verify is
  // in flight so the failure notice is known before navigating.
  useEffect(() => {
    if (verifying) return;
    if (state.status === "authenticated" || state.status === "refreshing") {
      void navigate({ href: redirectTo, replace: true });
    } else if (state.status === "unauthenticated") {
      void navigate({ href: loginHref(redirectTo, failureNotice.current), replace: true });
    }
  }, [state.status, verifying, navigate, redirectTo]);

  useEffect(() => {
    if (focusRequest === 0) return;
    (mode === "totp" ? codeRef : recoveryRef).current?.focus();
  }, [focusRequest, mode]);

  const verify = async (value: string, method: MFAMethod) => {
    if (verifying) return;
    setError(undefined);
    setVerifying(true);
    try {
      await submitMFA(value, method);
    } catch (err) {
      // A non-AppError reached here either never left the browser (the
      // challenge is still pending, so this notice goes unused) or came
      // from the session check after a successful verify — not a bad code.
      if (!isAppError(err)) failureNotice.current = "session_failed";
      else failureNotice.current = err.httpStatus === 423 ? "mfa_locked" : "mfa_failed";
      // A server rejection has spent the token and moved the machine to
      // unauthenticated; the redirect effect takes over. Only a request that
      // never reached the server leaves the challenge pending to retry.
      if (!isAppError(err)) {
        setCode("");
        setError("Couldn't verify the code. Check your connection and try again.");
        setFocusRequest((n) => n + 1);
      }
    } finally {
      setVerifying(false);
    }
  };

  const handleSubmit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (mode === "totp") {
      if (code.length !== TOTP_LENGTH) {
        setError(`Enter the ${TOTP_LENGTH}-digit code.`);
        return;
      }
      void verify(code, "totp");
      return;
    }
    const normalized = normalizeRecoveryCode(recoveryCode);
    if (!normalized) {
      setError("Enter a recovery code in the form XXXXX-XXXXX.");
      return;
    }
    void verify(normalized, "recovery_code");
  };

  const switchMode = (next: Mode) => {
    setMode(next);
    setError(undefined);
    setFocusRequest((n) => n + 1);
  };

  if (state.status !== "mfa_required") return null;

  if (!canTOTP && !canRecovery) {
    return (
      <AuthLayout>
        <h1 className="mb-4 font-semibold text-text text-xl">Two-factor authentication</h1>
        <p className="mb-6 text-sm text-text-secondary">
          Your account's verification method can't be used on this page yet.
        </p>
        <a href={loginHref(redirectTo)} className={linkButtonClassName}>
          Back to sign in
        </a>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 font-semibold text-text text-xl">Enter your verification code</h1>

      <form noValidate onSubmit={handleSubmit} className="flex flex-col gap-4">
        {mode === "totp" ? (
          <VerificationCodeInput
            ref={codeRef}
            label="Verification code"
            description="Enter the 6-digit code from your authenticator app."
            value={code}
            onChange={(next) => {
              setCode(next);
              setError(undefined);
            }}
            onComplete={(completed) => void verify(completed, "totp")}
            error={error}
            disabled={verifying}
            autoFocus
          />
        ) : (
          <div className="flex flex-col gap-1">
            <label htmlFor={recoveryId} className="text-sm text-text">
              Recovery code
            </label>
            <span id={`${recoveryId}-description`} className="text-sm text-text-secondary">
              Enter one of the recovery codes you saved when you set up two-factor authentication.
            </span>
            <input
              ref={recoveryRef}
              id={recoveryId}
              // biome-ignore lint/a11y/noAutofocus: the code field is this page's single task, same as VerificationCodeInput's autoFocus.
              autoFocus
              type="text"
              autoComplete="one-time-code"
              autoCorrect="off"
              autoCapitalize="characters"
              spellCheck={false}
              value={recoveryCode}
              disabled={verifying}
              aria-invalid={error ? true : undefined}
              aria-describedby={`${recoveryId}-description${error ? ` ${recoveryId}-error` : ""}`}
              onChange={(e) => {
                setRecoveryCode(e.target.value);
                setError(undefined);
              }}
              className={fieldInputClassName(Boolean(error), "input", "sans")}
            />
            {error && (
              <span id={`${recoveryId}-error`} role="alert" className="text-danger text-sm">
                {error}
              </span>
            )}
          </div>
        )}

        <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
          {verifying ? "Verifying…" : null}
        </div>

        <button
          type="submit"
          disabled={verifying}
          aria-busy={verifying}
          className={`${actionButtonClassName("primary", "md")} w-full justify-center`}
        >
          {verifying && <Spinner size={16} />}
          Verify
        </button>

        {canTOTP && canRecovery && (
          <button
            type="button"
            disabled={verifying}
            onClick={() => switchMode(mode === "totp" ? "recovery_code" : "totp")}
            className={linkButtonClassName}
          >
            {mode === "totp" ? "Use a recovery code instead" : "Use your authenticator app instead"}
          </button>
        )}
      </form>
    </AuthLayout>
  );
}
