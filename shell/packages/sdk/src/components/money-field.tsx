import type { ChangeEvent, ReactNode } from "react";
import { useId } from "react";
import { currencyMinorUnitDigits } from "./field.js";

export interface MoneyFieldProps {
  label?: string | undefined;
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
  value,
  currency,
  onChange,
  error,
  disabled = false,
  min,
  max,
}: MoneyFieldProps): ReactNode {
  const id = useId();
  // ISO 4217 decimal places vary (XOF 0, USD 2, KWD 3); default to 2 when
  // no currency has been resolved yet so the input still has a sane step.
  const digits = currency ? currencyMinorUnitDigits(currency) : 2;
  const handleChange = (e: ChangeEvent<HTMLInputElement>) => onChange(toMinorUnits(e.target.value, digits));

  return (
    <div>
      {label !== undefined && <label htmlFor={id}>{label}</label>}
      <span>
        {currency !== undefined && <span>{currency} </span>}
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
        />
      </span>
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
