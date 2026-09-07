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
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <label htmlFor={id} className="text-sm text-text">
          {label}
        </label>
      )}
      <input
        id={id}
        type="color"
        value={value || "#000000"}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(e.target.value)}
        className={`h-8 w-10 rounded-control border transition-colors duration-(--duration-fast) ease-out focus:border-primary focus:shadow-focus disabled:cursor-not-allowed disabled:opacity-50 ${
          error !== undefined ? "border-danger" : "border-border"
        }`}
      />
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
