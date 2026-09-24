import type { ReactNode } from "react";
import { useId, useState } from "react";
import { FieldError, FieldLabel } from "./field-wrapper.js";
import { IconButton } from "./icon-button.js";
import { InputBox } from "./text-input.js";

export interface PasswordFieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  // No default: current-password vs. new-password changes password-manager
  // autofill/generation behavior, and a wrong silent default is worse than
  // forcing every call site to state its context explicitly.
  autoComplete: "current-password" | "new-password";
  error?: string | undefined;
  disabled?: boolean | undefined;
  onBlur?: (() => void) | undefined;
}

export function PasswordField({
  label,
  value,
  onChange,
  autoComplete,
  error,
  disabled = false,
  onBlur,
}: PasswordFieldProps): ReactNode {
  const id = useId();
  const errorId = `${id}-error`;
  const [revealed, setRevealed] = useState(false);

  return (
    <div className="flex flex-col gap-1">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <InputBox
        id={id}
        type={revealed ? "text" : "password"}
        autoComplete={autoComplete}
        value={value}
        disabled={disabled}
        invalid={error !== undefined || undefined}
        aria-describedby={error !== undefined ? errorId : undefined}
        onChange={onChange}
        onBlur={onBlur}
        end={
          <IconButton
            icon={revealed ? "eye-off" : "eye"}
            label={revealed ? "Hide password" : "Show password"}
            size="sm"
            // Native mousedown-focuses-button behavior would otherwise move
            // focus off the input on click (password-field.md States table).
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => setRevealed((v) => !v)}
            disabled={disabled}
          />
        }
      />
      {error !== undefined && <FieldError id={errorId}>{error}</FieldError>}
    </div>
  );
}
