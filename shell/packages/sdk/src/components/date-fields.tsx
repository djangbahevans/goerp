import type { ChangeEvent, ReactNode } from "react";
import { useId } from "react";

// Native <input type="date"|"datetime-local"|"time"> value formats.
// Distinct components per form-view-types.ts's FieldType, matching the
// generic form renderer's date/datetime/time split — not a mode flag on
// one component.

function toDateInputValue(date: Date | undefined): string {
  return date ? date.toISOString().slice(0, 10) : "";
}

function toDateTimeInputValue(date: Date | undefined): string {
  return date ? date.toISOString().slice(0, 16) : "";
}

function parseDate(raw: string): Date | undefined {
  if (raw === "") return undefined;
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

export interface DateFieldProps {
  label?: string | undefined;
  value: Date | undefined;
  onChange: (date: Date) => void;
  min?: Date | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export function DateField({ label, value, onChange, min, error, disabled = false }: DateFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="date"
        value={toDateInputValue(value)}
        min={toDateInputValue(min) || undefined}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e: ChangeEvent<HTMLInputElement>) => {
          const date = parseDate(e.target.value);
          if (date) onChange(date);
        }}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}

export interface DateTimeFieldProps {
  label?: string | undefined;
  value: Date | undefined;
  onChange: (date: Date) => void;
  min?: Date | undefined;
  max?: Date | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export function DateTimeField({
  label,
  value,
  onChange,
  min,
  max,
  error,
  disabled = false,
}: DateTimeFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="datetime-local"
        value={toDateTimeInputValue(value)}
        min={toDateTimeInputValue(min) || undefined}
        max={toDateTimeInputValue(max) || undefined}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e: ChangeEvent<HTMLInputElement>) => {
          const date = parseDate(e.target.value);
          if (date) onChange(date);
        }}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}

export interface TimeFieldProps {
  label?: string | undefined;
  value: string | undefined;
  onChange: (value: string) => void;
  min?: string | undefined;
  max?: string | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export function TimeField({ label, value, onChange, min, max, error, disabled = false }: TimeFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="time"
        value={value ?? ""}
        min={min}
        max={max}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
