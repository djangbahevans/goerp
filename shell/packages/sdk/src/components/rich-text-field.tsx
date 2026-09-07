import type { ReactNode } from "react";
import { useId } from "react";
import { fieldInputClassName } from "./field-input-styles.js";

export interface RichTextFieldProps {
  label?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  // manifest-spec.md's FormField.rows ("Valid for textarea, rich_text").
  rows?: number | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// field-renderers.tsx's "rich_text" field type falls back to a plain
// textarea — no rich-text editor library wired in yet. Same posture here.
export function RichTextField({
  label,
  value,
  onChange,
  rows = 3,
  error,
  disabled = false,
}: RichTextFieldProps): ReactNode {
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
        value={value}
        rows={rows}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(e.target.value)}
        className={fieldInputClassName(error !== undefined, "input", "sans")}
      />
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
