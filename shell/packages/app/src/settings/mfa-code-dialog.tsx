import type { MFACodeConfirmation } from "@goerp/sdk/auth";
import {
  Button,
  FieldWrapper,
  Icon,
  MODAL_CONTENT_CLASSES,
  MODAL_OVERLAY_CLASSES,
  TextInput,
} from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { normalizeRecoveryCode } from "../auth/mfa-challenge-page.js";
import { VerificationCodeInput } from "../auth/verification-code-input.js";

const TOTP_LENGTH = 6;

type Mode = MFACodeConfirmation["type"];

function defaultErrorMessage(err: unknown, mode: Mode): string {
  if (isAppError(err) && err.code === "invalid_mfa_code") {
    return mode === "totp"
      ? "Incorrect code. Check your authenticator app and try again."
      : "That recovery code is incorrect or has already been used.";
  }
  if (isAppError(err) && (err.code === "mfa_locked" || err.httpStatus === 423)) {
    return "Too many incorrect codes. Try again in 15 minutes.";
  }
  return "Couldn't verify the code. Check your connection and try again.";
}

export interface MFACodeFormProps {
  submitLabel: string;
  submitVariant?: "primary" | "danger" | undefined;
  // Rejects to show an inline error and keep the form open.
  onSubmit: (confirmation: MFACodeConfirmation) => Promise<void>;
  // Overrides the inline message for a rejection; undefined falls back to
  // the invalid-code, lockout and connection messages.
  describeError?: ((err: unknown) => string | undefined) | undefined;
  onCancel?: (() => void) | undefined;
}

// A current 2FA code — six digits from the authenticator app, or a recovery
// code — as on the MFA challenge page (shell-ux.md §2.6).
export function MFACodeForm({
  submitLabel,
  submitVariant = "primary",
  onSubmit,
  describeError,
  onCancel,
}: MFACodeFormProps): ReactNode {
  const [mode, setMode] = useState<Mode>("totp");
  const [code, setCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [focusRequest, setFocusRequest] = useState(0);
  const codeRef = useRef<HTMLInputElement>(null);
  const recoveryRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (focusRequest === 0) return;
    (mode === "totp" ? codeRef : recoveryRef).current?.focus();
  }, [focusRequest, mode]);

  const submit = async (confirmation: MFACodeConfirmation) => {
    if (busy) return;
    setError(undefined);
    setBusy(true);
    try {
      await onSubmit(confirmation);
    } catch (err) {
      setError(describeError?.(err) ?? defaultErrorMessage(err, confirmation.type));
      if (confirmation.type === "totp") setCode("");
      setFocusRequest((n) => n + 1);
    } finally {
      setBusy(false);
    }
  };

  const handleSubmit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (mode === "totp") {
      if (code.length !== TOTP_LENGTH) {
        setError(`Enter the ${TOTP_LENGTH}-digit code.`);
        return;
      }
      void submit({ type: "totp", code });
      return;
    }
    const normalized = normalizeRecoveryCode(recoveryCode);
    if (!normalized) {
      setError("Enter a recovery code in the form XXXXX-XXXXX.");
      return;
    }
    void submit({ type: "recovery_code", code: normalized });
  };

  const switchMode = () => {
    setMode(mode === "totp" ? "recovery_code" : "totp");
    setError(undefined);
    setFocusRequest((n) => n + 1);
  };

  return (
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
          onComplete={(completed) => void submit({ type: "totp", code: completed })}
          error={error}
          disabled={busy}
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
            disabled={busy}
            onChange={(next) => {
              setRecoveryCode(next);
              setError(undefined);
            }}
          />
        </FieldWrapper>
      )}

      <div className="flex justify-start text-sm">
        <Button variant="link" disabled={busy} onClick={switchMode}>
          {mode === "totp" ? "Use a recovery code instead" : "Use your authenticator app instead"}
        </Button>
      </div>

      <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
        {busy ? "Verifying…" : null}
      </div>

      <div className="flex justify-end gap-2">
        {onCancel && (
          <Button variant="ghost" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" variant={submitVariant} loading={busy}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

export interface MFADialogProps {
  open: boolean;
  title: string;
  description?: string | undefined;
  tone?: "default" | "warning" | undefined;
  // Escape and the close paths call this; false while the dialog must stay
  // up, such as while new recovery codes are on screen unsaved.
  onDismiss: () => void;
  dismissible?: boolean | undefined;
  children: ReactNode;
}

// AlertDialog's panel and motion (components/alert-dialog.md) with a custom
// body. AlertDialog itself takes at most one text or select input, while
// these confirmations need the code field, its recovery-code switch, and an
// inline error that keeps the dialog open.
export function MFADialog({
  open,
  title,
  description,
  tone = "default",
  onDismiss,
  dismissible = true,
  children,
}: MFADialogProps): ReactNode {
  // Open-controlled with no Trigger, so Radix has no element to return focus
  // to; captured and restored by hand, as AlertDialog does.
  const triggerRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (open) triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  }, [open]);

  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={(next) => {
        if (!next && dismissible) onDismiss();
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          role="alertdialog"
          aria-modal="true"
          className={MODAL_CONTENT_CLASSES}
          onPointerDownOutside={(event) => event.preventDefault()}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
          {...(description ? {} : { "aria-describedby": undefined })}
        >
          <div className="flex max-h-[90vh] w-full max-w-120 flex-col rounded-structural bg-surface shadow-lg">
            <div className="flex items-start gap-3 px-6 pt-6">
              {tone === "warning" && (
                <Icon name="triangle-alert" size={20} className="mt-0.5 shrink-0 text-warning" aria-hidden="true" />
              )}
              <DialogPrimitive.Title className="font-semibold text-lg text-text">{title}</DialogPrimitive.Title>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-6 pt-4 pb-6">
              {description && (
                <DialogPrimitive.Description className="mb-4 text-base text-text-secondary">
                  {description}
                </DialogPrimitive.Description>
              )}
              {children}
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
