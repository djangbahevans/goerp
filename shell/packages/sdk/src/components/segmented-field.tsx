import type { ReactNode } from "react";
import { useId } from "react";

export interface SegmentedFieldOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SegmentedFieldProps {
  options: SegmentedFieldOption[];
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean | undefined;
}

export function SegmentedField({ options, value, onChange, disabled = false }: SegmentedFieldProps): ReactNode {
  const name = useId();
  return (
    <div className="inline-flex rounded-md border border-border">
      {options.map((option) => (
        <label key={option.value} data-selected={option.value === value}>
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={option.value === value}
            disabled={disabled || option.disabled}
            onChange={() => onChange(option.value)}
          />
          {option.label}
        </label>
      ))}
    </div>
  );
}
