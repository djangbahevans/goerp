import type { ReactNode } from "react";
import { useId } from "react";
import { fieldInputClassName } from "./field-input-styles.js";

export interface CodeFieldProps {
  label?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  // manifest-spec.md's FormField.language ("Use language for syntax
  // language") — surfaced as a data attribute rather than driving syntax
  // highlighting; no code-editor library wired in yet.
  language?: string | undefined;
  rows?: number | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// field-renderers.tsx's "code" field type falls back to a monospace
// textarea — no editor library wired in yet. Same posture here.
export function CodeField({
  label,
  value,
  onChange,
  language,
  rows = 3,
  error,
  disabled = false,
}: CodeFieldProps): ReactNode {
  const id = useId();
  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <label htmlFor={id} className="text-sm text-text">
          {label}
        </label>
      )}
      <textarea
        id={id}
        data-language={language}
        value={value}
        rows={rows}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(e.target.value)}
        className={fieldInputClassName(error !== undefined)}
      />
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
