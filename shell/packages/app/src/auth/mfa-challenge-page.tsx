import {
  type MFAMethod,
  type MFAVerification,
  requestPasskeyAssertion,
  supportsPasskeys,
  useAuth,
} from "@goerp/sdk/auth";
import { Button, FieldWrapper, TextInput, TextLink } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
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

// Recovery codes are hashed in XXXXX-XXXXX form; malformed input must not spend the single-use MFA token.
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

export function MFAChallengePage({ redirectTo }: MFAChallengePageProps): ReactNode {
  const { state, submitMFA } = useAuth();
  const navigate = useNavigate();
  const codeRef = useRef<HTMLInputElement>(null);
  const recoveryRef = useRef<HTMLInputElement>(null);

  const methods: MFAMethod[] = state.status === "mfa_required" ? state.methods : [];
  const canTOTP = methods.includes("totp");
  const canRecovery = methods.includes("recovery_code");
  const canPasskey = methods.includes("webauthn") && supportsPasskeys();
  const passkeyAbort = useRef<AbortController | null>(null);
  const passkeyButton = useRef<HTMLButtonElement>(null);
  const restorePasskeyFocus = useRef(false);

  useEffect(() => () => passkeyAbort.current?.abort(), []);

  const [mode, setMode] = useState<Mode>(canTOTP ? "totp" : "recovery_code");
  const [code, setCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [passkeyError, setPasskeyError] = useState<string | undefined>();
  const [verifying, setVerifying] = useState(false);
  const [awaitingPasskey, setAwaitingPasskey] = useState(false);
  useEffect(() => {
    if (!verifying && restorePasskeyFocus.current) {
      restorePasskeyFocus.current = false;
      passkeyButton.current?.focus();
    }
  }, [verifying]);
  // The failure notice must be set before navigation observes the unauthenticated state.
  const failureNotice = useRef<LoginNotice | undefined>(undefined);
  const [focusRequest, setFocusRequest] = useState(0);

  // Navigation waits for verification cleanup so the failure notice is available.
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

  const submit = async (confirmation: MFAVerification) => {
    try {
      await submitMFA(confirmation);
    } catch (err) {
      // Session-reload failure after successful verification must not be reported as an invalid factor.
      if (!isAppError(err)) failureNotice.current = "session_failed";
      else failureNotice.current = err.httpStatus === 423 ? "mfa_locked" : "mfa_failed";
      // Server rejection spends the token; a request that never reaches the server can be retried.
      if (!isAppError(err)) {
        if (confirmation.type === "webauthn") {
          setPasskeyError("Couldn't verify the passkey. Check your connection and try again.");
        } else {
          setCode("");
          setError("Couldn't verify the code. Check your connection and try again.");
          setFocusRequest((n) => n + 1);
        }
      }
    }
  };

  const verify = async (value: string, method: "totp" | "recovery_code") => {
    if (verifying) return;
    setError(undefined);
    setPasskeyError(undefined);
    setVerifying(true);
    try {
      await submit({ type: method, code: value });
    } finally {
      setVerifying(false);
    }
  };

  const verifyPasskey = async () => {
    if (verifying || state.status !== "mfa_required") return;
    setError(undefined);
    setPasskeyError(undefined);
    setVerifying(true);
    const controller = new AbortController();
    passkeyAbort.current = controller;
    restorePasskeyFocus.current = true;
    setAwaitingPasskey(true);
    try {
      const assertion = await requestPasskeyAssertion({ mfaToken: state.challengeToken, signal: controller.signal });
      setAwaitingPasskey(false);
      if (assertion && !controller.signal.aborted) await submit(assertion);
    } catch (err) {
      setPasskeyError(
        isAppError(err) && err.code === "mfa_locked"
          ? "Too many failed verification attempts. Try again later."
          : "Couldn't use your passkey. Try again or choose another method.",
      );
    } finally {
      setVerifying(false);
      setAwaitingPasskey(false);
      passkeyAbort.current = null;
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

  if (!canTOTP && !canRecovery && !canPasskey) {
    return (
      <AuthLayout>
        <h1 className="mb-4 font-semibold text-text text-xl">Two-factor authentication</h1>
        <p className="mb-6 text-sm text-text-secondary">
          Your account's verification method can't be used on this page yet.
        </p>
        <p className="text-sm">
          <TextLink href={loginHref(redirectTo)}>Back to sign in</TextLink>
        </p>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 font-semibold text-text text-xl">
        {canTOTP || canRecovery ? "Enter your verification code" : "Two-factor authentication"}
      </h1>

      {canPasskey && (
        <div className="mb-4 flex flex-col gap-3">
          <Button
            ref={passkeyButton}
            variant="primary"
            fullWidth
            loading={verifying}
            onClick={() => void verifyPasskey()}
          >
            Use a passkey
          </Button>
          {awaitingPasskey && (
            <Button variant="ghost" onClick={() => passkeyAbort.current?.abort()}>
              Cancel passkey
            </Button>
          )}
          {passkeyError && (
            <p role="alert" className="text-danger text-sm">
              {passkeyError}
            </p>
          )}
        </div>
      )}

      {(canTOTP || canRecovery) && (
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
            <FieldWrapper
              label="Recovery code"
              description="Enter one of the recovery codes you saved when you set up two-factor authentication."
              error={error || undefined}
            >
              <TextInput
                ref={recoveryRef}
                autoFocus
                autoComplete="one-time-code"
                autoCorrect="off"
                autoCapitalize="characters"
                spellCheck={false}
                value={recoveryCode}
                disabled={verifying}
                onChange={(next) => {
                  setRecoveryCode(next);
                  setError(undefined);
                }}
              />
            </FieldWrapper>
          )}

          <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
            {verifying ? "Verifying…" : null}
          </div>

          <Button type="submit" variant="primary" fullWidth loading={verifying}>
            Verify
          </Button>

          {canTOTP && canRecovery && (
            <div className="flex justify-center text-sm">
              <Button
                variant="link"
                disabled={verifying}
                onClick={() => switchMode(mode === "totp" ? "recovery_code" : "totp")}
              >
                {mode === "totp" ? "Use a recovery code instead" : "Use your authenticator app instead"}
              </Button>
            </div>
          )}
        </form>
      )}
    </AuthLayout>
  );
}
