import type { ReactNode } from "react";
import { useId } from "react";

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
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <textarea
        id={id}
        value={value}
        rows={rows}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
