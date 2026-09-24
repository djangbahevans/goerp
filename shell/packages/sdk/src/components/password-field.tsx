import type { ChangeEvent, ReactNode } from "react";
import { useId, useState } from "react";
import { fieldInputClassName } from "./field-input-styles.js";

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
  const [revealed, setRevealed] = useState(false);

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-sm text-text">
        {label}
      </label>
      <span className={`inline-flex items-center gap-2 ${fieldInputClassName(error !== undefined, "wrapper", "sans")}`}>
        <input
          id={id}
          type={revealed ? "text" : "password"}
          autoComplete={autoComplete}
          value={value}
          disabled={disabled}
          aria-invalid={error !== undefined}
          onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value)}
          onBlur={onBlur}
          className="w-full border-0 bg-transparent p-0 text-sm focus:outline-none"
        />
        <button
          type="button"
          // Native mousedown-focuses-button behavior would otherwise move
          // focus off the input on click, violating "toggling never moves
          // focus away from the input" (password-field.md States table).
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => setRevealed((v) => !v)}
          disabled={disabled}
          aria-label={revealed ? "Hide password" : "Show password"}
          className="rounded-control p-1 text-text-secondary transition-colors duration-(--duration-fast) ease-out hover:text-text focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50 motion-reduce:transition-none"
        >
          {revealed ? "Hide" : "Show"}
        </button>
      </span>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
