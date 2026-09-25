import type { ReactNode } from "react";
import { useState } from "react";
import { CodeSelect, CodeSelectFallbackInput, supportedValuesOrEmpty } from "./code-select.js";

export interface TimezoneSelectProps {
  id?: string | undefined;
  // IANA timezone identifier (e.g. "America/New_York").
  value?: string | undefined;
  onChange: (id: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
  // A first option standing for "no zone of its own", e.g. "Organisation
  // default (UTC)"; selecting it calls onChange("").
  emptyLabel?: string | undefined;
}

// Display/search/sort formatting only — not a resolved name (Intl.DisplayNames
// has no "timeZone" type). A raw IANA id's underscore reads oddly and a typed
// "new york" wouldn't otherwise match "America/New_York".
function spacedTimezoneName(id: string): string {
  return id.replaceAll("_", " ");
}

export function TimezoneSelect({
  id,
  value,
  onChange,
  placeholder,
  disabled = false,
  emptyLabel,
}: TimezoneSelectProps): ReactNode {
  // Lazy useState, not a module-level constant — keeps codes reference-stable
  // across this instance's own re-renders (CodeSelect's entries memoization
  // depends on it) while still calling Intl.supportedValuesOf fresh per
  // mount, so a test can mock it before rendering.
  const [codes] = useState(() => supportedValuesOrEmpty("timeZone"));

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
      nameOf={spacedTimezoneName}
      renderRow={(_code, name) => <span className="font-mono">{name}</span>}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      disabled={disabled}
      emptyLabel={emptyLabel}
    />
  );
}
