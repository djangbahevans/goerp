import type { ReactNode } from "react";
import { CodeSelect } from "./code-select.js";
import { fieldInputClassName } from "./field-input-styles.js";

export interface TimezoneSelectProps {
  id?: string | undefined;
  // IANA timezone identifier (e.g. "America/New_York").
  value?: string | undefined;
  onChange: (id: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

// Intl.supportedValuesOf enumerates the full valid set at runtime, unlike
// country/language — no bundled static dataset needed. Computed once at
// module scope (not per-render/per-mount) so CodeSelect's own `codes`-keyed
// memoization sees a reference-stable array.
function supportedTimezonesOrEmpty(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}

const TIMEZONE_CODES = supportedTimezonesOrEmpty();

// Display/search/sort formatting only — not a resolved name (Intl.DisplayNames
// has no "timeZone" type). A raw IANA id's underscore reads oddly and a typed
// "new york" wouldn't otherwise match "America/New_York".
function spacedTimezoneName(id: string): string {
  return id.replaceAll("_", " ");
}

export function TimezoneSelect({ id, value, onChange, placeholder, disabled = false }: TimezoneSelectProps): ReactNode {
  if (TIMEZONE_CODES.length === 0) {
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
      codes={TIMEZONE_CODES}
      nameOf={spacedTimezoneName}
      renderRow={(_code, name) => <span className="font-mono">{name}</span>}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      disabled={disabled}
    />
  );
}
