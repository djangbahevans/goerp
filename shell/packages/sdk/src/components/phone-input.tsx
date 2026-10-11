import { type CountryCode, getCountries, getCountryCallingCode } from "libphonenumber-js/min";
import type { ReactNode } from "react";
import { useId, useMemo, useState } from "react";
import { Combobox } from "./combobox.js";
import { CountryFlag, countryNameOf } from "./country-flag.js";
import { FieldControlProvider, joinIds, useFieldControl } from "./field-wrapper.js";
import { TextInput, type TextInputProps } from "./text-input.js";

export interface PhoneInputProps
  extends Omit<TextInputProps, "type" | "start" | "end" | "size" | "autoComplete" | "inputMode"> {
  countryLabel?: string | undefined;
}

const COUNTRIES = getCountries().map((country) => ({
  country,
  callingCode: getCountryCallingCode(country),
  label: `${countryNameOf(country)} (+${getCountryCallingCode(country)})`,
}));
COUNTRIES.sort((a, b) => a.label.localeCompare(b.label));

const CALLING_CODES = [...new Set(COUNTRIES.map((entry) => entry.callingCode))];

function splitPhone(value: string): { callingCode: string | undefined; number: string } {
  const callingCode = value.startsWith("+") ? CALLING_CODES.find((code) => value.startsWith(`+${code}`)) : undefined;
  return {
    callingCode,
    number: callingCode ? value.slice(callingCode.length + 1).replace(/^[\s.-]+/, "") : value,
  };
}

function composePhone(callingCode: string | undefined, number: string): string {
  if (number === "" || callingCode === undefined) return number;
  return `+${callingCode} ${number}`;
}

export function PhoneInput({
  value,
  onChange,
  countryLabel = "Country calling code",
  disabled = false,
  readOnly = false,
  invalid,
  id,
  "aria-describedby": describedBy,
  ...inputProps
}: PhoneInputProps): ReactNode {
  const field = useFieldControl();
  const generatedId = useId();
  const countryId = `${generatedId}-country`;
  const [query, setQuery] = useState("");
  // An empty value cannot carry a country; shared calling codes cannot identify one.
  const [selection, setSelection] = useState<{ value: string; country: CountryCode }>();
  if (selection !== undefined && selection.value !== value) setSelection(undefined);
  const split = splitPhone(value);
  const explicitCountry = selection?.value === value ? selection.country : undefined;
  const candidates = COUNTRIES.filter((entry) => entry.callingCode === split.callingCode);
  const country = explicitCountry ?? (candidates.length === 1 ? candidates[0]?.country : undefined);
  const callingCode = country ? getCountryCallingCode(country) : split.callingCode;
  const selected = COUNTRIES.find((entry) => entry.country === country);
  const search = query.trim().toLowerCase();
  const options = useMemo(
    () =>
      COUNTRIES.filter(
        (entry) => entry.label.toLowerCase().includes(search) || entry.country.toLowerCase().includes(search),
      ),
    [search],
  );

  function chooseCountry(nextCountry: CountryCode): void {
    const nextCode = getCountryCallingCode(nextCountry);
    // An unrecognized international prefix stays visible as subscriber text when a country is chosen.
    const number = split.callingCode === undefined ? split.number.replace(/^\+/, "") : split.number;
    const next = composePhone(nextCode, number);
    setSelection({ value: next, country: nextCountry });
    if (next !== value) onChange(next);
  }

  function editNumber(number: string): void {
    const next = number.startsWith("+") ? number : composePhone(callingCode, number);
    setSelection(country && !number.startsWith("+") ? { value: next, country } : undefined);
    onChange(next);
  }

  return (
    <div className="flex min-w-0 flex-wrap gap-2">
      <div className="min-w-0 grow basis-44">
        <label htmlFor={countryId} className="sr-only">
          {countryLabel}
        </label>
        <FieldControlProvider
          value={{
            id: countryId,
            describedBy: joinIds(field?.describedBy, describedBy),
            invalid: invalid ?? field?.invalid ?? false,
            required: false,
          }}
        >
          <Combobox
            query={query}
            onQueryChange={setQuery}
            options={options}
            getOptionKey={(entry) => entry.country}
            getOptionLabel={(entry) => entry.label}
            renderOption={(entry) => (
              <span className="flex min-w-0 items-center gap-2">
                <span aria-hidden>
                  <CountryFlag code={entry.country} />
                </span>
                <span className="min-w-0 truncate">{entry.label}</span>
              </span>
            )}
            onSelect={(entry) => chooseCountry(entry.country)}
            selectedLabel={selected?.label ?? (callingCode ? `Calling code +${callingCode}` : undefined)}
            startAdornment={country ? <CountryFlag code={country} /> : undefined}
            placeholder="Choose country…"
            disabled={disabled || readOnly}
          />
        </FieldControlProvider>
      </div>
      <div className="min-w-0 grow basis-44">
        <TextInput
          {...inputProps}
          id={field?.id ?? id ?? generatedId}
          aria-describedby={describedBy}
          type="tel"
          inputMode="tel"
          autoComplete={callingCode ? "tel-national" : "tel"}
          value={split.number}
          onChange={editNumber}
          disabled={disabled}
          readOnly={readOnly}
          invalid={invalid}
        />
      </div>
    </div>
  );
}
