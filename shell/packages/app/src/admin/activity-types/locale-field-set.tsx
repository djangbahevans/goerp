import { FieldWrapper, TextInput } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { localeLabel } from "../settings/settings-section.js";

export interface LocaleFieldSetProps {
  label: string;
  value: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
  defaultLocale: string;
  otherLocales: string[];
  required?: boolean;
  error?: string | undefined;
  maxLength: number;
}

// shell-ux.md §5.10 "Type sheet" — one TextInput per locale, the tenant's
// default locale first and required, the rest under a "Translations"
// disclosure open whenever any of them already has a value. A locale
// cleared back to empty text is dropped from the map entirely, since
// scheduled-activities.md §9 "API" says PATCH replaces label/default_summary
// as whole maps and removing a translation means omitting it.
export function LocaleFieldSet({
  label,
  value,
  onChange,
  defaultLocale,
  otherLocales,
  required = false,
  error,
  maxLength,
}: LocaleFieldSetProps): ReactNode {
  function set(tag: string, text: string): void {
    if (text === "") {
      const { [tag]: _removed, ...rest } = value;
      onChange(rest);
    } else {
      onChange({ ...value, [tag]: text });
    }
  }

  const hasTranslation = otherLocales.some((tag) => (value[tag] ?? "") !== "");

  return (
    <div className="flex flex-col gap-2">
      <FieldWrapper label={label} required={required} error={error}>
        <TextInput
          value={value[defaultLocale] ?? ""}
          onChange={(text) => set(defaultLocale, text)}
          maxLength={maxLength}
        />
      </FieldWrapper>
      {otherLocales.length > 0 && (
        <details open={hasTranslation} className="group">
          <summary className="cursor-pointer text-sm text-text-secondary hover:text-text">Translations</summary>
          <div className="mt-2 flex flex-col gap-2">
            {otherLocales.map((tag) => (
              <FieldWrapper key={tag} label={localeLabel(tag)}>
                <TextInput value={value[tag] ?? ""} onChange={(text) => set(tag, text)} maxLength={maxLength} />
              </FieldWrapper>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}
