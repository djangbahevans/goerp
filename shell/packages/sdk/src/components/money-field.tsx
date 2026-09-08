import type { ChangeEvent, ReactNode } from "react";
import { useId } from "react";
import { currencyMinorUnitDigits } from "./field.js";
import { fieldInputClassName } from "./field-input-styles.js";

export interface MoneyFieldProps {
  label?: string | undefined;
  // Lets an external <label htmlFor> target the <input> directly (goerp#698).
  id?: string | undefined;
  // l10n-guide.md: monetary amounts are always integer minor units (e.g.
  // GHS pesewas) — 10000 here is GHS 100.00, never a decimal major-unit
  // amount. `min`/`max` are minor units too, same as `value`.
  value: number | undefined;
  // Static code, or a value resolved from another field at render time
  // (manifest `currency_field`) — this component only ever sees the
  // resolved string, same as `Field`'s `currency` prop. Omitted entirely
  // when the bound record has no `currency_field` configured.
  currency?: string | undefined;
  onChange: (value: number | undefined) => void;
  error?: string | undefined;
  disabled?: boolean | undefined;
  min?: number | undefined;
  max?: number | undefined;
}

function toMajorUnits(minorUnits: number | undefined, digits: number): number | "" {
  return minorUnits === undefined ? "" : minorUnits / 10 ** digits;
}

function toMinorUnits(raw: string, digits: number): number | undefined {
  if (raw === "") return undefined;
  const n = Number(raw);
  return Number.isNaN(n) ? undefined : Math.round(n * 10 ** digits);
}

export function MoneyField({
  label,
  id: idProp,
  value,
  currency,
  onChange,
  error,
  disabled = false,
  min,
  max,
}: MoneyFieldProps): ReactNode {
  const generatedId = useId();
  const id = idProp ?? generatedId;
  // ISO 4217 decimal places vary (XOF 0, USD 2, KWD 3); default to 2 when
  // no currency has been resolved yet so the input still has a sane step.
  const digits = currency ? currencyMinorUnitDigits(currency) : 2;
  const handleChange = (e: ChangeEvent<HTMLInputElement>) => onChange(toMinorUnits(e.target.value, digits));

  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <label htmlFor={id} className="text-sm text-text">
          {label}
        </label>
      )}
      <span className={`inline-flex items-center gap-2 ${fieldInputClassName(error !== undefined, "wrapper")}`}>
        {currency !== undefined && <span className="font-sans text-sm text-text-secondary">{currency}</span>}
        <input
          id={id}
          type="number"
          step={1 / 10 ** digits}
          min={toMajorUnits(min, digits)}
          max={toMajorUnits(max, digits)}
          value={toMajorUnits(value, digits)}
          disabled={disabled}
          aria-invalid={error !== undefined}
          onChange={handleChange}
          className="w-full border-0 bg-transparent p-0 font-mono text-sm focus:outline-none"
        />
      </span>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
