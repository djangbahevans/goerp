import { createNumberFormatter, type FormatOptions, type NumberFormatter } from "./tenant-format.js";
import { localeStore } from "./use-locale.js";

function formatter(options: FormatOptions): NumberFormatter {
  const { locale = localeStore.getLocale(), ...intlOptions } = options;
  return createNumberFormatter(locale, intlOptions);
}

// l10n-guide.md: "All monetary amounts are stored and transmitted as
// integer minor units." (pesewas, cents, ...). The ISO 4217 decimal-place
// count varies per currency (XOF 0, USD 2, KWD 3), which Intl already
// resolves internally; reading it back out of resolvedOptions() avoids
// hardcoding a minor-unit table here.
export function currencyMinorUnitDigits(currency: string): number {
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2;
}

// typescript-sdk-reference.md "Formatting functions": the user's locale,
// with the tenant's number format for the separators.
export function formatNumber(value: number, options: FormatOptions = {}): string {
  return formatter(options).format(value);
}

export function formatPercent(value: number, options: FormatOptions = {}): string {
  return formatter({ maximumFractionDigits: 2, ...options, style: "percent" }).format(value);
}

// `minorUnits` is the integer amount in the currency's smallest unit.
export function formatCurrency(minorUnits: number, currency: string, options: FormatOptions = {}): string {
  const digits = currencyMinorUnitDigits(currency);
  return formatter({ ...options, style: "currency", currency }).format(minorUnits / 10 ** digits);
}
