import type { ChangeEvent, ReactNode } from "react";
import { useId } from "react";

// Native <input type="date"|"datetime-local"|"time"> value formats.
// Distinct components per form-view-types.ts's FieldType, matching the
// generic form renderer's date/datetime/time split — not a mode flag on
// one component.

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// Date-only strings parse as UTC midnight per ECMA-262, and toISOString()
// output is UTC — both sides of the date-only round-trip agree, so UTC
// components are fine here.
function toDateInputValue(date: Date | undefined): string {
  return date ? date.toISOString().slice(0, 10) : "";
}

// <input type="datetime-local">'s value string carries no timezone, so the
// browser (and `new Date(raw)` on the way back in) both treat it as local
// wall-clock time — formatting via toISOString() here would disagree with
// that and drift the displayed/stored instant by the local UTC offset on
// every edit. Local getters keep both directions consistent.
function toDateTimeInputValue(date: Date | undefined): string {
  if (!date) return "";
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}T${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

function parseDate(raw: string): Date | undefined {
  if (raw === "") return undefined;
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

export interface DateFieldProps {
  label?: string | undefined;
  value: Date | undefined;
  // Called with undefined when the input is cleared, matching `value`'s own
  // optionality — a caller that doesn't care can ignore that case.
  onChange: (date: Date | undefined) => void;
  min?: Date | undefined;
  max?: Date | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export function DateField({ label, value, onChange, min, max, error, disabled = false }: DateFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="date"
        value={toDateInputValue(value)}
        min={toDateInputValue(min) || undefined}
        max={toDateInputValue(max) || undefined}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(parseDate(e.target.value))}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}

export interface DateTimeFieldProps {
  label?: string | undefined;
  value: Date | undefined;
  onChange: (date: Date | undefined) => void;
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
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(parseDate(e.target.value))}
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
