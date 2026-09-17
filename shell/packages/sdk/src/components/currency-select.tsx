import type { ReactNode } from "react";
import { CodeSelect } from "./code-select.js";
import { fieldInputClassName } from "./field-input-styles.js";

export interface CurrencySelectProps {
  id?: string | undefined;
  // ISO 4217 currency code (e.g. "USD", "EUR").
  value?: string | undefined;
  onChange: (code: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

// Mirrors timezone-select.tsx's supportedTimezonesOrEmpty — computed once at
// module scope for a reference-stable array. No display formatting needed:
// ISO 4217 codes are flat three-letter strings with no separators to
// normalize, and no name/symbol resolution (a symbol like "$" is ambiguous
// across currencies; the code alone is both cheaper and clearer).
function supportedCurrenciesOrEmpty(): string[] {
  try {
    return Intl.supportedValuesOf("currency");
  } catch {
    return [];
  }
}

const CURRENCY_CODES = supportedCurrenciesOrEmpty();

function identity(code: string): string {
  return code;
}

export function CurrencySelect({ id, value, onChange, placeholder, disabled = false }: CurrencySelectProps): ReactNode {
  if (CURRENCY_CODES.length === 0) {
    return (
      <input
        id={id}
        type="text"
        className={fieldInputClassName(false, "input", "sans")}
        value={value ?? ""}
        disabled={disabled}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  return (
    <CodeSelect
      id={id}
      codes={CURRENCY_CODES}
      nameOf={identity}
      renderRow={(code) => <span className="font-mono">{code}</span>}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      disabled={disabled}
    />
  );
}
