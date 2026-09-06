import type { ChangeEvent, ReactNode } from "react";
import { useId } from "react";

// Native <input type="date"|"datetime-local"|"time"> value formats.
// Distinct components per form-view-types.ts's FieldType, matching the
// generic form renderer's date/datetime/time split — not a mode flag on
// one component.

function normalizeDateBound(bound: Date | string | undefined, kind: "date" | "datetime-local"): string | undefined {
  if (bound === undefined) return undefined;
  const date = bound instanceof Date ? bound : new Date(bound);
  if (Number.isNaN(date.getTime())) return typeof bound === "string" ? bound : undefined;
  const iso = date.toISOString();
  return kind === "date" ? iso.slice(0, 10) : iso.slice(0, 16);
}

interface DateLikeFieldProps {
  label?: string | undefined;
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

export interface DateFieldProps extends DateLikeFieldProps {
  min?: Date | string | undefined;
  max?: Date | string | undefined;
}

export type DateTimeFieldProps = DateLikeFieldProps;

export type TimeFieldProps = DateLikeFieldProps;

function handleInputChange(onChange: (value: string | undefined) => void) {
  return (e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value === "" ? undefined : e.target.value);
}

export function DateField({ label, value, onChange, error, disabled = false, min, max }: DateFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="date"
        value={value ?? ""}
        min={normalizeDateBound(min, "date")}
        max={normalizeDateBound(max, "date")}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={handleInputChange(onChange)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}

export function DateTimeField({ label, value, onChange, error, disabled = false }: DateTimeFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="datetime-local"
        value={value ?? ""}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={handleInputChange(onChange)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}

export function TimeField({ label, value, onChange, error, disabled = false }: TimeFieldProps): ReactNode {
  const id = useId();
  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <input
        id={id}
        type="time"
        value={value ?? ""}
        disabled={disabled}
        aria-invalid={error !== undefined}
        onChange={handleInputChange(onChange)}
      />
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
