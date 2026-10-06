import { useSyncExternalStore } from "react";

// l10n-guide.md §2 "Tenant default locale": the tenant's `l10n.number_format`
// and `l10n.first_day_of_week`, as `GET /auth/me`'s `tenant` reports them.
export type NumberFormatPattern = "1,234.56" | "1.234,56" | "1 234,56";
export type FirstDayOfWeek = "monday" | "sunday";

export interface TenantFormat {
  numberFormat: NumberFormatPattern;
  firstDayOfWeek: FirstDayOfWeek;
}

export const DEFAULT_TENANT_FORMAT: TenantFormat = { numberFormat: "1,234.56", firstDayOfWeek: "monday" };

// The group separator of "1 234,56" is a no-break space, so a number never
// wraps between its digit groups.
const SEPARATORS: Record<NumberFormatPattern, { group: string; decimal: string }> = {
  "1,234.56": { group: ",", decimal: "." },
  "1.234,56": { group: ".", decimal: "," },
  "1 234,56": { group: " ", decimal: "," },
};

type Listener = () => void;

// Module-level singleton like LocaleStore: AuthProvider sets it from the
// session, and the formatting functions, which are not hooks, read it.
export class TenantFormatStore {
  private format: TenantFormat = DEFAULT_TENANT_FORMAT;
  private readonly listeners = new Set<Listener>();

  get = (): TenantFormat => this.format;

  set(next: TenantFormat): void {
    if (next.numberFormat === this.format.numberFormat && next.firstDayOfWeek === this.format.firstDayOfWeek) return;
    this.format = { ...next };
    for (const listener of this.listeners) listener();
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const tenantFormatStore = new TenantFormatStore();

export type TenantFormatStoreLike = Pick<TenantFormatStore, "get" | "subscribe">;

export function useTenantFormat(store: TenantFormatStoreLike = tenantFormatStore): TenantFormat {
  return useSyncExternalStore(store.subscribe, store.get);
}

// The part of Intl.NumberFormat the tenant's separators can honour.
export interface NumberFormatter {
  format(value: number | bigint): string;
  formatToParts(value: number | bigint): Intl.NumberFormatPart[];
  resolvedOptions(): Intl.ResolvedNumberFormatOptions;
}

export interface FormatOptions extends Intl.NumberFormatOptions {
  // BCP 47; defaults to the user's current locale.
  locale?: string;
}

// The user's locale decides digits, grouping sizes, currency symbol and its
// placement; only the group and decimal separators are the tenant's, so a
// user reading Arabic still sees Eastern Arabic digits with the tenant's
// separators between them.
export function createNumberFormatter(
  locale: string | undefined,
  options: Intl.NumberFormatOptions = {},
  pattern: NumberFormatPattern = tenantFormatStore.get().numberFormat,
): NumberFormatter {
  const intl = new Intl.NumberFormat(locale, options);
  const separators = SEPARATORS[pattern];
  const formatToParts = (value: number | bigint) =>
    intl.formatToParts(value).map((part) => {
      if (part.type === "group") return { ...part, value: separators.group };
      if (part.type === "decimal") return { ...part, value: separators.decimal };
      return part;
    });
  return {
    format: (value) => formatToParts(value).reduce((text, part) => text + part.value, ""),
    formatToParts,
    resolvedOptions: () => intl.resolvedOptions(),
  };
}
