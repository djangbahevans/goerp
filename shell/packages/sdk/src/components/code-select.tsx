import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import { Combobox } from "./combobox.js";
import { TextInput } from "./text-input.js";

// Shared combobox mechanics behind country-select.tsx, language-select.tsx,
// timezone-select.tsx, and currency-select.tsx — same searchable-list-over-
// a-dataset shape, differing only in dataset, name resolution, and row
// visual.

export interface CodeSelectProps {
  id?: string | undefined;
  codes: string[];
  nameOf: (code: string) => string;
  renderRow: (code: string, name: string) => ReactNode;
  // Decorative icon overlaid on the closed trigger, left of its plain-text
  // value — an <input>'s own value can't hold rich child content the way
  // an open row can, so the closed state can't literally nest renderRow.
  leadingIcon?: ((code: string) => ReactNode) | undefined;
  value?: string | undefined;
  onChange: (code: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
  // Names the empty value as a real choice, e.g. "Organisation default
  // (UTC)": it's pinned first in the list, shown in the trigger instead of
  // the placeholder, and selecting it calls onChange("").
  emptyLabel?: string | undefined;
}

interface Entry {
  code: string;
  name: string;
}

// Shared by timezone-select.tsx/currency-select.tsx, whose datasets are
// enumerated at runtime instead of bundled like country/language.
export function supportedValuesOrEmpty(key: "currency" | "timeZone"): string[] {
  try {
    return Intl.supportedValuesOf(key);
  } catch {
    return [];
  }
}

export interface CodeSelectFallbackProps {
  id?: string | undefined;
  value?: string | undefined;
  onChange: (value: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

// Rendered instead of CodeSelect when supportedValuesOrEmpty comes back
// empty (Intl.supportedValuesOf unavailable) — a plain free-text input,
// the terminal degrade CountrySelect/LanguageSelect never need since their
// bundled datasets are always populated.
export function CodeSelectFallbackInput({
  id,
  value,
  onChange,
  placeholder,
  disabled = false,
}: CodeSelectFallbackProps): ReactNode {
  return <TextInput id={id} value={value ?? ""} disabled={disabled} placeholder={placeholder} onChange={onChange} />;
}

export function CodeSelect({
  id,
  codes,
  nameOf,
  renderRow,
  leadingIcon,
  value,
  onChange,
  placeholder,
  disabled = false,
  emptyLabel,
}: CodeSelectProps): ReactNode {
  const [query, setQuery] = useState("");

  // The resolved locale doesn't change while mounted, so names+sort order
  // are computed once per dataset.
  const entries = useMemo<Entry[]>(() => {
    const withNames = codes.map((code): Entry => ({ code, name: nameOf(code) }));
    withNames.sort((a, b) => a.name.localeCompare(b.name));
    return emptyLabel === undefined ? withNames : [{ code: "", name: emptyLabel }, ...withNames];
  }, [codes, nameOf, emptyLabel]);

  const normalizedQuery = query.trim().toLowerCase();
  const matches = useMemo(() => {
    if (normalizedQuery === "") return entries;
    return entries.filter(
      (entry) =>
        entry.name.toLowerCase().includes(normalizedQuery) || entry.code.toLowerCase().includes(normalizedQuery),
    );
  }, [entries, normalizedQuery]);

  // A value outside the bundled dataset (legacy data, a code the standard
  // gained after this list was written) still needs to display and stay
  // clearable, not silently vanish — falls back to nameOf's own
  // raw-code degrade, same as an unresolvable Intl.DisplayNames lookup.
  const selected = value
    ? (entries.find((entry) => entry.code === value) ?? { code: value, name: nameOf(value) })
    : emptyLabel !== undefined
      ? { code: "", name: emptyLabel }
      : undefined;
  // The labelled empty value is itself the cleared state.
  const clearable = selected !== undefined && selected.code !== "";

  return (
    <Combobox<Entry>
      id={id}
      query={query}
      onQueryChange={setQuery}
      options={matches}
      getOptionKey={(entry) => entry.code || "(empty)"}
      getOptionLabel={(entry) => entry.name}
      renderOption={(entry) => renderRow(entry.code, entry.name)}
      onSelect={(entry) => onChange(entry.code)}
      selectedLabel={selected?.name}
      onClear={clearable ? () => onChange("") : undefined}
      startAdornment={clearable && selected && leadingIcon ? leadingIcon(selected.code) : undefined}
      disabled={disabled}
      placeholder={placeholder}
    />
  );
}
