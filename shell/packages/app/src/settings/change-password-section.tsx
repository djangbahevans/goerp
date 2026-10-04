import { GLOBAL_PASSWORD_MIN_LENGTH, passwordMinLengthFrom, useAuth } from "@goerp/sdk/auth";
import { Button, PasswordField, PasswordStrengthMeter, SectionCard } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useLocation } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { PASSWORD_MISMATCH, policyMessageAsSentence } from "../auth/password-messages.js";

export const CHANGE_PASSWORD_ANCHOR = "change-password";

interface FieldErrors {
  current?: string | undefined;
  next?: string | undefined;
  confirm?: string | undefined;
}

export function ChangePasswordSection({
  restricted = false,
  onChanged,
  onBusyChange,
}: {
  restricted?: boolean;
  onChanged?: (() => Promise<void>) | undefined;
  onBusyChange?: ((busy: boolean) => void) | undefined;
}): ReactNode {
  const { changePassword, user } = useAuth();
  const hash = useLocation({ select: (location) => location.hash });
  const sectionRef = useRef<HTMLDivElement>(null);

  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);
  // A 422's details.min_length is the rule the engine applied, should the
  // tenant's policy have changed since the session was loaded.
  const [rejectedMinLength, setRejectedMinLength] = useState<number | null>(null);
  const minLength = rejectedMinLength ?? user?.passwordMinLength ?? GLOBAL_PASSWORD_MIN_LENGTH;

  useEffect(() => {
    if (!restricted && hash !== CHANGE_PASSWORD_ANCHOR) return;
    const section = sectionRef.current;
    if (!section) return;
    section.scrollIntoView?.({ block: "start" });
    section.querySelector<HTMLInputElement>('input[autocomplete="current-password"]')?.focus();
  }, [hash, restricted]);

  const handleConfirmBlur = () => {
    setErrors((e) => ({ ...e, confirm: confirm && confirm !== next ? PASSWORD_MISMATCH : undefined }));
  };

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting) return;

    const found: FieldErrors = {};
    if (!current) found.current = "Enter your current password.";
    if (!next) found.next = "Enter a new password.";
    if (!confirm) found.confirm = "Confirm your new password.";
    else if (confirm !== next) found.confirm = PASSWORD_MISMATCH;
    setErrors(found);
    if (Object.keys(found).length > 0) return;

    setSubmitting(true);
    onBusyChange?.(true);
    try {
      await changePassword({ currentPassword: current, newPassword: next });
      setCurrent("");
      setNext("");
      setConfirm("");
      toast.success("Password updated. Other sessions were signed out.");
      await onChanged?.();
    } catch (err) {
      if (isAppError(err) && err.code === "invalid_password") {
        setCurrent("");
        setErrors({ current: "Current password is incorrect." });
      } else if (isAppError(err) && err.code === "auth.password_too_weak") {
        setRejectedMinLength(passwordMinLengthFrom(err) ?? rejectedMinLength);
        setErrors({ next: policyMessageAsSentence(err.message) });
      } else {
        toast.error("Couldn't change your password. Try again.");
      }
    } finally {
      setSubmitting(false);
      onBusyChange?.(false);
    }
  };

  return (
    <div id={CHANGE_PASSWORD_ANCHOR} ref={sectionRef} className="scroll-mt-(--space-4)">
      <SectionCard title="Change password">
        {restricted && (
          <p className="mb-4 text-sm text-text-secondary">
            Changing your password signs you out of your other sessions in every organisation.
          </p>
        )}
        <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
          <PasswordField
            label="Current password"
            autoComplete="current-password"
            value={current}
            onChange={setCurrent}
            error={errors.current}
            disabled={submitting}
          />
          <div className="flex flex-col gap-2">
            <PasswordField
              label="New password"
              autoComplete="new-password"
              minLength={minLength}
              value={next}
              onChange={setNext}
              error={errors.next}
              disabled={submitting}
            />
            <PasswordStrengthMeter password={next} minLength={minLength} />
          </div>
          <PasswordField
            label="Confirm new password"
            autoComplete="new-password"
            value={confirm}
            onChange={setConfirm}
            onBlur={handleConfirmBlur}
            error={errors.confirm}
            disabled={submitting}
          />
          <div>
            <Button type="submit" loading={submitting}>
              Change password
            </Button>
          </div>
        </form>
      </SectionCard>
    </div>
  );
}
