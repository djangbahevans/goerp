import { PasswordField, PasswordStrengthMeter } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { PASSWORD_MISMATCH } from "./password-messages.js";

const TOO_WEAK = "Choose a stronger password: at least 12 characters, rated Fair or better.";

export interface NewPasswordErrors {
  next?: string | undefined;
  confirm?: string | undefined;
}

// shell-ux.md §2.4/§2.5's client-side rules: refuse under 12 characters or
// below strength score 2 (strongEnough, from the meter), and require the
// confirmation to match.
// noun is "password" where there's no old one to replace (registration).
export function validateNewPassword(
  next: string,
  confirm: string,
  strongEnough: boolean,
  noun = "new password",
): NewPasswordErrors {
  const errors: NewPasswordErrors = {};
  if (!next) errors.next = `Enter a ${noun}.`;
  else if (!strongEnough) errors.next = TOO_WEAK;
  if (!confirm) errors.confirm = `Confirm your ${noun}.`;
  else if (confirm !== next) errors.confirm = PASSWORD_MISMATCH;
  return errors;
}

// The mismatch shows when the confirm field loses focus, not per keystroke.
export function confirmBlurError(next: string, confirm: string): string | undefined {
  return confirm && confirm !== next ? PASSWORD_MISMATCH : undefined;
}

export interface NewPasswordFieldsProps {
  next: string;
  confirm: string;
  onNextChange: (value: string) => void;
  onConfirmChange: (value: string) => void;
  onConfirmBlur: () => void;
  onStrengthChange: (strongEnough: boolean) => void;
  errors: NewPasswordErrors;
  disabled?: boolean | undefined;
  nextLabel?: string | undefined;
  confirmLabel?: string | undefined;
}

export function NewPasswordFields({
  next,
  confirm,
  onNextChange,
  onConfirmChange,
  onConfirmBlur,
  onStrengthChange,
  errors,
  disabled,
  nextLabel = "New password",
  confirmLabel = "Confirm new password",
}: NewPasswordFieldsProps): ReactNode {
  return (
    <>
      <div className="flex flex-col gap-2">
        <PasswordField
          label={nextLabel}
          autoComplete="new-password"
          value={next}
          onChange={onNextChange}
          error={errors.next}
          disabled={disabled}
        />
        <PasswordStrengthMeter password={next} onValidityChange={onStrengthChange} />
      </div>
      <PasswordField
        label={confirmLabel}
        autoComplete="new-password"
        value={confirm}
        onChange={onConfirmChange}
        onBlur={onConfirmBlur}
        error={errors.confirm}
        disabled={disabled}
      />
    </>
  );
}
