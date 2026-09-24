import type { ReactNode } from "react";
import { useId } from "react";
import { useFieldControl } from "./field-wrapper.js";

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
  const field = useFieldControl();
  // A FieldWrapper's <label htmlFor> can only target one radio: the checked
  // one (or the first), which is where Tab lands in the group. It carries
  // the rest of the wiring too.
  const labelledValue = options.some((option) => option.value === value) ? value : options[0]?.value;
  return (
    <div className="inline-flex divide-x divide-border overflow-hidden rounded-control border border-border">
      {options.map((option) => (
        <label
          key={option.value}
          data-selected={option.value === value}
          className="cursor-pointer px-3 py-2 text-sm transition-colors duration-(--duration-fast) ease-out has-focus-visible:shadow-focus has-disabled:cursor-not-allowed has-disabled:opacity-50 data-[selected=true]:bg-primary data-[selected=true]:text-text-inverse data-[selected=false]:bg-surface data-[selected=false]:text-text-secondary"
        >
          <input
            {...(option.value === labelledValue && field
              ? {
                  id: field.id,
                  "aria-describedby": field.describedBy,
                  "aria-invalid": field.invalid || undefined,
                  required: field.required || undefined,
                }
              : {})}
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
