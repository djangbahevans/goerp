import type { ReactNode } from "react";
import { useId } from "react";

export interface ColorPickerProps {
  label?: string | undefined;
  value?: string | undefined;
  onChange: (value: string) => void;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// Matches field-renderers.tsx's "color_picker" field type exactly: a native
// <input type="color">, defaulting to black when unset.
export function ColorPicker({ label, value, onChange, error, disabled = false }: ColorPickerProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="color"
        value={value || "#000000"}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
