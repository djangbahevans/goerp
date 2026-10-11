import * as Popover from "@radix-ui/react-popover";
import { type CountryCode, getCountries, getCountryCallingCode } from "libphonenumber-js/min";
import type { ReactNode } from "react";
import { useId, useMemo, useRef, useState } from "react";
import { CountryFlag, countryNameOf } from "./country-flag.js";
import { FieldControlProvider, joinIds, useFieldControl } from "./field-wrapper.js";
import { useFloatingPanelLayer } from "./floating-panel.js";
import { Icon } from "./icon.js";
import { useListboxNavigation } from "./listbox-navigation.js";
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
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const tabTarget = useRef<"phone" | "trigger" | null>(null);
  const layer = useFloatingPanelLayer(triggerRef);
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

  const isOpen = open && !disabled && !readOnly;
  const nav = useListboxNavigation({
    count: options.length,
    isOpen,
    homeEnd: query === "",
    onChoose: (index) => {
      const entry = options[index];
      if (entry) chooseCountry(entry.country);
    },
  });

  function changeOpen(next: boolean): void {
    setOpen(next);
    if (next) {
      setQuery("");
      nav.setActiveIndex(
        Math.max(
          0,
          COUNTRIES.findIndex((entry) => entry.country === country),
        ),
      );
    }
  }

  function chooseCountry(nextCountry: CountryCode): void {
    const nextCode = getCountryCallingCode(nextCountry);
    // An unrecognized international prefix stays visible as subscriber text when a country is chosen.
    const number = split.callingCode === undefined ? split.number.replace(/^\+/, "") : split.number;
    const next = composePhone(nextCode, number);
    setSelection({ value: next, country: nextCountry });
    if (next !== value) onChange(next);
    changeOpen(false);
  }

  function editNumber(number: string): void {
    const next = number.startsWith("+") ? number : composePhone(callingCode, number);
    setSelection(country && !number.startsWith("+") ? { value: next, country } : undefined);
    onChange(next);
  }

  const countryValue = selected?.label ?? (callingCode ? `Calling code +${callingCode}` : "Choose country");

  return (
    <div ref={rootRef} className="min-w-0">
      <span id={`${countryId}-label`} className="sr-only">
        {countryLabel}
      </span>
      <span id={`${countryId}-value`} className="sr-only">
        {countryValue}
      </span>
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
        start={
          <Popover.Root open={isOpen} onOpenChange={changeOpen}>
            <Popover.Trigger asChild>
              <button
                ref={triggerRef}
                id={countryId}
                type="button"
                disabled={disabled || readOnly}
                aria-labelledby={`${countryId}-label ${countryId}-value`}
                aria-describedby={joinIds(field?.describedBy, describedBy)}
                aria-invalid={(invalid ?? field?.invalid) || undefined}
                className="-ms-3 flex h-9 shrink-0 items-center gap-2 rounded-s-control border-border-control border-e px-3 text-sm text-text hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none disabled:cursor-not-allowed max-md:h-11"
                onKeyDown={(event) => {
                  if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
                    event.preventDefault();
                    event.stopPropagation();
                    changeOpen(true);
                  }
                }}
              >
                <span aria-hidden className="flex items-center">
                  {country ? <CountryFlag code={country} /> : <Icon name="globe" size={16} aria-hidden />}
                </span>
                {callingCode && <span>+{callingCode}</span>}
                <Icon name="chevron-down" size={16} aria-hidden />
              </button>
            </Popover.Trigger>
            <Popover.Portal>
              <Popover.Content
                aria-label={countryLabel}
                align="start"
                sideOffset={4}
                collisionPadding={8}
                className={`${layer} flex max-h-(--radix-popover-content-available-height) w-72 max-w-[calc(100vw-1rem)] flex-col gap-2 rounded-structural border border-border bg-surface p-2 shadow-md`}
                onMouseDown={(event) => {
                  event.stopPropagation();
                  if (event.target === event.currentTarget) event.preventDefault();
                }}
                onOpenAutoFocus={(event) => {
                  event.preventDefault();
                  searchRef.current?.focus();
                }}
                onCloseAutoFocus={(event) => {
                  if (tabTarget.current) {
                    event.preventDefault();
                    if (tabTarget.current === "phone")
                      rootRef.current?.querySelector<HTMLInputElement>('input[type="tel"]')?.focus();
                    else triggerRef.current?.focus();
                    tabTarget.current = null;
                  }
                }}
                onEscapeKeyDown={(event) => {
                  event.preventDefault();
                  changeOpen(false);
                }}
                onKeyDown={(event) => {
                  event.stopPropagation();
                  if (event.key === "Tab") {
                    event.preventDefault();
                    tabTarget.current = event.shiftKey ? "trigger" : "phone";
                    changeOpen(false);
                  }
                }}
              >
                <FieldControlProvider
                  value={{ id: `${countryId}-search`, describedBy: undefined, required: false, invalid: false }}
                >
                  <TextInput
                    {...nav.inputProps}
                    ref={searchRef}
                    aria-label="Search countries"
                    placeholder="Search countries…"
                    value={query}
                    onChange={(next) => {
                      setQuery(next);
                      nav.setActiveIndex(0);
                    }}
                    onKeyDown={(event) => {
                      nav.handleKeyDown(event);
                      if (event.key === "Enter" && !event.nativeEvent.isComposing) event.preventDefault();
                    }}
                  />
                </FieldControlProvider>
                <div
                  id={nav.listboxId}
                  role="listbox"
                  aria-label={countryLabel}
                  className="max-h-80 min-h-0 overflow-y-auto"
                >
                  {options.map((entry, index) => (
                    <div
                      key={entry.country}
                      {...nav.getOptionProps(index, { selected: entry.country === country })}
                      className={`flex cursor-pointer items-center gap-2 rounded-control px-2 py-2 text-sm text-text max-md:min-h-11 ${index === nav.activeIndex ? "bg-surface-hover" : ""}`}
                    >
                      <span aria-hidden>
                        <CountryFlag code={entry.country} />
                      </span>
                      <span className="min-w-0 flex-1">{entry.label}</span>
                      {entry.country === country && <Icon name="check" size={16} aria-hidden />}
                    </div>
                  ))}
                  {options.length === 0 && <p className="px-2 py-2 text-sm text-text-secondary">No countries found</p>}
                </div>
                <span role="status" className="sr-only">
                  {options.length} {options.length === 1 ? "country" : "countries"} found
                </span>
              </Popover.Content>
            </Popover.Portal>
          </Popover.Root>
        }
      />
    </div>
  );
}
