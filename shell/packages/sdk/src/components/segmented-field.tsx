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
    <div className="inline-flex divide-x divide-border overflow-hidden rounded-control border border-border">
      {options.map((option) => (
        <label
          key={option.value}
          data-selected={option.value === value}
          className="cursor-pointer px-3 py-2 text-sm transition-colors duration-(--duration-fast) ease-out has-focus-visible:shadow-focus has-disabled:cursor-not-allowed has-disabled:opacity-50 data-[selected=true]:bg-primary data-[selected=true]:text-text-inverse data-[selected=false]:bg-surface data-[selected=false]:text-text-secondary"
        >
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={option.value === value}
            disabled={disabled || option.disabled}
            onChange={() => onChange(option.value)}
            className="sr-only"
          />
          {option.label}
        </label>
      ))}
    </div>
  );
}
