import type { MFACodeConfirmation, TOTPEnrollment, TOTPEnrollmentConfirmation } from "@goerp/sdk/auth";
import { Button, Checkbox, FieldWrapper, Spinner, TextInput } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, type SubmitEvent, useCallback, useEffect, useRef, useState } from "react";
import { RecoveryCodesList, TOTPScanDetails } from "../auth/totp-enrollment-parts.js";
import { VerificationCodeInput } from "../auth/verification-code-input.js";
import { SideSheet } from "../chrome/side-sheet.js";
import { MFACodeForm } from "./mfa-code-dialog.js";

export interface AddAuthenticatorClient {
  begin: () => Promise<TOTPEnrollment>;
  confirm: (input: TOTPEnrollmentConfirmation) => Promise<string[] | null>;
  reverify: (confirmation: MFACodeConfirmation) => Promise<void>;
}

export interface AddAuthenticatorSheetProps {
  open: boolean;
  client: AddAuthenticatorClient;
  // The factor is enrolled, before any recovery codes are shown.
  onEnrolled: () => void;
  onClose: () => void;
}

const CODE_LENGTH = 6;

type Pending = { kind: "begin" } | { kind: "confirm"; input: TOTPEnrollmentConfirmation; enrollment: TOTPEnrollment };

type Step =
  | { kind: "starting"; notice?: string }
  | { kind: "start_failed" }
  | { kind: "reverify"; retry: Pending }
  | { kind: "scan"; enrollment: TOTPEnrollment; notice?: string }
  | { kind: "codes"; codes: string[] };

// The user already holds a factor here, so the enrollment calls can ask the
// session to prove MFA again first (auth-internals.md §8 "MFA enrollment").
function needsReverify(err: unknown): boolean {
  return isAppError(err) && (err.code === "mfa_reverify_required" || err.code === "mfa_required");
}

// shell-ux.md §4.3 "Add authenticator app": §2.7's TOTP steps in a
// dismissable sheet.
export function AddAuthenticatorSheet({ open, client, onEnrolled, onClose }: AddAuthenticatorSheetProps): ReactNode {
  // A fresh flow, and so a fresh pending enrollment, each time the sheet opens.
  const [run, setRun] = useState(0);
  // New recovery codes are on screen and not yet marked saved: they're shown
  // only once, so the sheet stays open until the user confirms.
  const [holdingCodes, setHoldingCodes] = useState(false);
  useEffect(() => {
    if (!open) return;
    setRun((n) => n + 1);
    setHoldingCodes(false);
  }, [open]);

  return (
    <SideSheet
      open={open}
      onClose={() => {
        if (!holdingCodes) onClose();
      }}
      title="Add authenticator app"
    >
      {run > 0 && (
        <AddAuthenticatorFlow
          key={run}
          client={client}
          onEnrolled={onEnrolled}
          onHoldingCodes={setHoldingCodes}
          onDone={onClose}
        />
      )}
    </SideSheet>
  );
}

interface FlowProps {
  client: AddAuthenticatorClient;
  onEnrolled: () => void;
  onHoldingCodes: (holding: boolean) => void;
  onDone: () => void;
}

function AddAuthenticatorFlow({ client, onEnrolled, onHoldingCodes, onDone }: FlowProps): ReactNode {
  const [step, setStep] = useState<Step>({ kind: "starting" });
  const [label, setLabel] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const codeRef = useRef<HTMLInputElement>(null);
  const started = useRef(false);

  const start = useCallback(
    async (notice?: string) => {
      setStep({ kind: "starting", ...(notice ? { notice } : {}) });
      setCode("");
      setError(undefined);
      try {
        const enrollment = await client.begin();
        setStep({ kind: "scan", enrollment, ...(notice ? { notice } : {}) });
      } catch (err) {
        setStep(needsReverify(err) ? { kind: "reverify", retry: { kind: "begin" } } : { kind: "start_failed" });
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

  const confirm = async (input: TOTPEnrollmentConfirmation, enrollment: TOTPEnrollment) => {
    setError(undefined);
    setBusy(true);
    let codes: string[] | null;
    try {
      codes = await client.confirm(input);
    } catch (err) {
      setBusy(false);
      setCode("");
      if (needsReverify(err)) {
        setStep({ kind: "reverify", retry: { kind: "confirm", input, enrollment } });
        return;
      }
      if (isAppError(err) && err.code === "mfa_enrollment_not_found") {
        void start("That setup expired or had too many attempts. Scan this new QR code.");
        return;
      }
      setStep({ kind: "scan", enrollment });
      setError(
        isAppError(err) && err.code === "invalid_mfa_code"
          ? "Incorrect code. Check your authenticator app and try again."
          : "Couldn't verify the code. Check your connection and try again.",
      );
      codeRef.current?.focus();
      return;
    }
    setBusy(false);
    onEnrolled();
    if (codes && codes.length > 0) {
      setStep({ kind: "codes", codes });
      onHoldingCodes(true);
    } else {
      onDone();
    }
  };

  const submitCode = (value: string) => {
    if (busy || step.kind !== "scan") return;
    const trimmed = label.trim();
    void confirm(
      { enrollmentId: step.enrollment.enrollmentId, code: value, ...(trimmed ? { label: trimmed } : {}) },
      step.enrollment,
    );
  };

  const handleSubmit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (code.length !== CODE_LENGTH) {
      setError(`Enter the ${CODE_LENGTH}-digit code.`);
      return;
    }
    submitCode(code);
  };

  if (step.kind === "starting") {
    return (
      <div role="status" className="flex items-center gap-2 p-4 text-sm text-text-secondary">
        <Spinner size={16} />
        Preparing your setup…
      </div>
    );
  }

  if (step.kind === "start_failed") {
    return (
      <div className="flex flex-col items-start gap-4 p-4">
        <p role="alert" className="text-danger text-sm">
          Couldn't start setting up an authenticator app. Check your connection and try again.
        </p>
        <Button variant="primary" onClick={() => void start()}>
          Try again
        </Button>
      </div>
    );
  }

  if (step.kind === "reverify") {
    const { retry } = step;
    return (
      <div className="flex flex-col gap-4 p-4">
        <p className="text-sm text-text-secondary">
          Confirm it's you before adding another authenticator app. Enter a code from one you already use.
        </p>
        <MFACodeForm
          submitLabel="Continue"
          onSubmit={async (confirmation) => {
            await client.reverify(confirmation);
            if (retry.kind === "begin") {
              void start();
              return;
            }
            setStep({ kind: "scan", enrollment: retry.enrollment });
            void confirm(retry.input, retry.enrollment);
          }}
        />
      </div>
    );
  }

  if (step.kind === "codes") {
    return (
      <div className="flex flex-col gap-4 p-4">
        <p className="text-sm text-text-secondary">
          Authenticator app added. Save these recovery codes somewhere safe. Each one signs you in once if you lose your
          authenticator app, and they won't be shown again.
        </p>
        <RecoveryCodesList codes={step.codes} />
        <Checkbox
          label="I've saved these codes"
          checked={saved}
          onChange={(next) => {
            setSaved(next);
            onHoldingCodes(!next);
          }}
        />
        <Button variant="primary" fullWidth disabled={!saved} onClick={onDone}>
          Done
        </Button>
      </div>
    );
  }

  return (
    <form noValidate onSubmit={handleSubmit} className="flex flex-col gap-4 p-4">
      <p className="text-sm text-text-secondary">
        Scan this QR code with an authenticator app, then enter the 6-digit code it shows.
      </p>
      {step.notice && (
        <p role="alert" className="text-sm text-warning">
          {step.notice}
        </p>
      )}
      <TOTPScanDetails enrollment={step.enrollment} />
      <FieldWrapper
        label="Name"
        description="Optional. Helps you tell your authenticator apps apart, e.g. “Work phone”."
      >
        <TextInput value={label} maxLength={64} disabled={busy} onChange={setLabel} />
      </FieldWrapper>
      <VerificationCodeInput
        ref={codeRef}
        label="Verification code"
        description="Enter the 6-digit code from your authenticator app."
        value={code}
        onChange={(next) => {
          setCode(next);
          setError(undefined);
        }}
        onComplete={submitCode}
        error={error}
        disabled={busy}
      />
      <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
        {busy ? "Verifying…" : null}
      </div>
      <Button type="submit" variant="primary" fullWidth loading={busy}>
        Add authenticator app
      </Button>
    </form>
  );
}
