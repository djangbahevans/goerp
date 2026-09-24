import { beginTOTPEnrollment, confirmTOTPEnrollment, type TOTPEnrollment, useAuth } from "@goerp/sdk/auth";
import { Button, Spinner } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useCallback, useEffect, useId, useRef, useState } from "react";
import { AuthLayout } from "./auth-layout.js";
import { VerificationCodeInput } from "./verification-code-input.js";

// Injectable for stories; the route uses the real auth client.
export interface MFASetupClient {
  begin: () => Promise<TOTPEnrollment>;
  confirm: (enrollmentId: string, code: string) => Promise<string[] | null>;
}

const defaultClient: MFASetupClient = {
  begin: beginTOTPEnrollment,
  confirm: (enrollmentId, code) => confirmTOTPEnrollment({ enrollmentId, code }),
};

export interface MFASetupPageProps {
  // Already passed through safeRedirect.
  redirectTo: string;
  client?: MFASetupClient | undefined;
}

const CODE_LENGTH = 6;
const RECOVERY_FILE_NAME = "goerp-recovery-codes.txt";

type Step =
  | { kind: "starting"; notice?: string }
  | { kind: "start_failed" }
  | { kind: "scan"; enrollment: TOTPEnrollment; notice?: string }
  | { kind: "codes"; codes: string[] }
  // Enrolled, with no new recovery codes to show; finishing may need a retry.
  | { kind: "done" };

// Groups the base32 key in fours so it can be read off and typed by hand.
export function formatManualKey(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(" ") ?? secret;
}

function svgDataURL(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

async function copyText(text: string, what: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(`${what} copied.`);
  } catch {
    toast.error(`Couldn't copy the ${what.toLowerCase()}. Select it and copy it manually.`);
  }
}

function downloadCodes(codes: string[]): void {
  const blob = new Blob([`${codes.join("\n")}\n`], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = RECOVERY_FILE_NAME;
  a.click();
  URL.revokeObjectURL(url);
}

// shell-ux.md §2.7: TOTP setup the tenant's MFA policy requires before the
// user can do anything else. Step 1 scans and verifies; step 2 shows the
// recovery codes issued with the first factor.
export function MFASetupPage({ redirectTo, client = defaultClient }: MFASetupPageProps): ReactNode {
  const { reloadSession, logout } = useAuth();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>({ kind: "starting" });
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [finishError, setFinishError] = useState<string | undefined>(undefined);
  const codeRef = useRef<HTMLInputElement>(null);
  const savedId = useId();
  const started = useRef(false);

  const start = useCallback(
    async (notice?: string) => {
      setStep({ kind: "starting", ...(notice ? { notice } : {}) });
      setCode("");
      setError(undefined);
      try {
        const enrollment = await client.begin();
        setStep({ kind: "scan", enrollment, ...(notice ? { notice } : {}) });
      } catch {
        setStep({ kind: "start_failed" });
      }
    },
    [client],
  );

  // StrictMode mounts effects twice; one pending enrollment is enough.
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void start();
  }, [start]);

  const finish = async () => {
    setBusy(true);
    setFinishError(undefined);
    try {
      await reloadSession();
      await navigate({ href: redirectTo, replace: true });
    } catch {
      setFinishError("Couldn't finish setup. Check your connection and try again.");
      setBusy(false);
    }
  };

  const confirm = async (value: string) => {
    if (busy || step.kind !== "scan") return;
    setError(undefined);
    setBusy(true);
    let codes: string[] | null;
    try {
      codes = await client.confirm(step.enrollment.enrollmentId, value);
    } catch (err) {
      setBusy(false);
      setCode("");
      if (isAppError(err) && err.code === "mfa_enrollment_not_found") {
        void start("That setup expired or had too many attempts. Scan this new QR code.");
        return;
      }
      setError(
        isAppError(err) && err.code === "invalid_mfa_code"
          ? "Incorrect code. Check your authenticator app and try again."
          : "Couldn't verify the code. Check your connection and try again.",
      );
      codeRef.current?.focus();
      return;
    }
    setBusy(false);
    if (codes && codes.length > 0) {
      setStep({ kind: "codes", codes });
    } else {
      setStep({ kind: "done" });
      void finish();
    }
  };

  const handleSubmit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (code.length !== CODE_LENGTH) {
      setError(`Enter the ${CODE_LENGTH}-digit code.`);
      return;
    }
    void confirm(code);
  };

  const signOut = async () => {
    await logout();
    await navigate({ to: "/auth/login", replace: true });
  };

  return (
    <AuthLayout>
      <h1 className="mb-2 font-semibold text-text text-xl">Your organisation requires two-factor authentication</h1>

      {step.kind === "starting" && (
        <div role="status" className="flex items-center gap-2 py-8 text-sm text-text-secondary">
          <Spinner size={16} />
          Preparing your setup…
        </div>
      )}

      {step.kind === "start_failed" && (
        <div className="flex flex-col gap-4">
          <p role="alert" className="text-danger text-sm">
            Couldn't start two-factor setup. Check your connection and try again.
          </p>
          <Button variant="primary" fullWidth onClick={() => void start()}>
            Try again
          </Button>
        </div>
      )}

      {step.kind === "scan" && (
        <form noValidate onSubmit={handleSubmit} className="flex flex-col gap-4">
          <p className="text-sm text-text-secondary">
            Step 1 of 2. Scan this QR code with an authenticator app, then enter the 6-digit code it shows.
          </p>
          {step.notice && (
            <p role="alert" className="text-sm text-warning">
              {step.notice}
            </p>
          )}
          <img
            src={svgDataURL(step.enrollment.qrSvg)}
            alt="QR code to add this account to your authenticator app"
            width={176}
            height={176}
            className="mx-auto rounded-control border border-border bg-white p-2"
          />
          <div className="flex flex-col gap-1">
            <span className="text-sm text-text">Can't scan it? Enter this key instead</span>
            <div className="flex items-center gap-2">
              <code className="flex-1 select-all break-all rounded-control bg-bg-subtle px-2 py-1 font-mono text-sm text-text">
                {formatManualKey(step.enrollment.secret)}
              </code>
              <Button
                size="sm"
                aria-label="Copy setup key"
                onClick={() => void copyText(step.enrollment.secret, "Key")}
              >
                Copy
              </Button>
            </div>
          </div>

          <VerificationCodeInput
            ref={codeRef}
            label="Verification code"
            description="Enter the 6-digit code from your authenticator app."
            value={code}
            onChange={(next) => {
              setCode(next);
              setError(undefined);
            }}
            onComplete={(completed) => void confirm(completed)}
            error={error}
            disabled={busy}
            autoFocus
          />

          <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
            {busy ? "Verifying…" : null}
          </div>

          <Button type="submit" variant="primary" fullWidth loading={busy}>
            Verify
          </Button>
        </form>
      )}

      {step.kind === "codes" && (
        <div className="flex flex-col gap-4">
          <p className="text-sm text-text-secondary">
            Step 2 of 2. Save these recovery codes somewhere safe. Each one signs you in once if you lose your
            authenticator app, and they won't be shown again.
          </p>
          <ol aria-label="Recovery codes" className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-control bg-bg-subtle p-3">
            {step.codes.map((recoveryCode) => (
              <li key={recoveryCode} className="font-mono text-sm text-text">
                {recoveryCode}
              </li>
            ))}
          </ol>
          <div className="flex gap-2">
            <Button size="sm" fullWidth onClick={() => void copyText(step.codes.join("\n"), "Recovery codes")}>
              Copy all
            </Button>
            <Button size="sm" fullWidth onClick={() => downloadCodes(step.codes)}>
              Download
            </Button>
          </div>
          <label htmlFor={savedId} className="flex items-center gap-2 text-sm text-text">
            <input
              id={savedId}
              type="checkbox"
              checked={saved}
              disabled={busy}
              onChange={(e) => setSaved(e.target.checked)}
              className="accent-primary"
            />
            I've saved these codes
          </label>
          {finishError && (
            <p role="alert" className="text-danger text-sm">
              {finishError}
            </p>
          )}
          <Button variant="primary" fullWidth disabled={!saved} loading={busy} onClick={() => void finish()}>
            Finish
          </Button>
        </div>
      )}

      {step.kind === "done" && (
        <div className="flex flex-col gap-4">
          <p className="text-sm text-text-secondary">Two-factor authentication is set up.</p>
          {finishError ? (
            <>
              <p role="alert" className="text-danger text-sm">
                {finishError}
              </p>
              <Button variant="primary" fullWidth loading={busy} onClick={() => void finish()}>
                Try again
              </Button>
            </>
          ) : (
            <div role="status" className="flex items-center gap-2 text-sm text-text-secondary">
              <Spinner size={16} />
              Finishing…
            </div>
          )}
        </div>
      )}

      <div className="mt-6 flex justify-center text-sm">
        <Button variant="link" onClick={() => void signOut()}>
          Sign out
        </Button>
      </div>
    </AuthLayout>
  );
}
