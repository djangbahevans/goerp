import type { ReactNode } from "react";
import { FieldError, useSelfLabelledFieldControl } from "./field-wrapper.js";

export interface ColorPickerProps {
  label?: string | undefined;
  id?: string | undefined;
  value?: string | undefined;
  onChange: (value: string) => void;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export function ColorPicker({
  label,
  id: idProp,
  value,
  onChange,
  error,
  disabled = false,
}: ColorPickerProps): ReactNode {
  const { id, errorId, controlProps } = useSelfLabelledFieldControl(idProp, error);
  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <label htmlFor={id} className="text-sm text-text">
          {label}
        </label>
      )}
      <input
        {...controlProps}
        type="color"
        value={value || "#000000"}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className={`h-9 w-10 rounded-control border transition-colors duration-(--duration-fast) ease-out focus:border-primary focus:shadow-focus disabled:cursor-not-allowed disabled:opacity-50 max-md:h-11 max-md:w-11 ${
          controlProps["aria-invalid"] ? "border-danger" : "border-border"
        }`}
      />
      {error !== undefined && <FieldError id={errorId}>{error}</FieldError>}
    </div>
  );
}
