import type { ReactNode } from "react";
import { useState } from "react";
import { CodeSelect, CodeSelectFallbackInput, supportedValuesOrEmpty } from "./code-select.js";

export interface CurrencySelectProps {
  id?: string | undefined;
  // ISO 4217 currency code (e.g. "USD", "EUR").
  value?: string | undefined;
  onChange: (code: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

function identity(code: string): string {
  return code;
}

export function CurrencySelect({ id, value, onChange, placeholder, disabled = false }: CurrencySelectProps): ReactNode {
  // See timezone-select.tsx's identical lazy useState for why this isn't a
  // module-level constant.
  const [codes] = useState(() => supportedValuesOrEmpty("currency"));

  if (codes.length === 0) {
    return (
      <CodeSelectFallbackInput
        id={id}
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        disabled={disabled}
      />
    );
  }

  return (
    <CodeSelect
      id={id}
      codes={codes}
      nameOf={identity}
      renderRow={(code) => <span className="font-mono">{code}</span>}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      disabled={disabled}
    />
  );
}
