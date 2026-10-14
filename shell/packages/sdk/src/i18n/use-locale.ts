import { useMemo, useSyncExternalStore } from "react";
import { createDateFormatter, type DatePreferencesStoreLike, datePreferencesStore } from "./date-format.js";
import {
  createNumberFormatter,
  type FirstDayOfWeek,
  type NumberFormatter,
  type TenantFormatStoreLike,
  tenantFormatStore,
  useTenantFormat,
} from "./tenant-format.js";

export type TextDirection = "ltr" | "rtl";

export const DEFAULT_LOCALE = "en";

// Read synchronously so document direction is correct before React's first render.
const STORAGE_KEY = "goerp-locale";

const RTL_SCRIPTS = new Set(["Adlm", "Arab", "Hebr", "Mand", "Nkoo", "Rohg", "Samr", "Syrc", "Thaa"]);

type Listener = (locale: string) => void;

function hasDocument(): boolean {
  return typeof document !== "undefined" && typeof window !== "undefined";
}

function canonicalize(locale: string): string | null {
  try {
    return Intl.getCanonicalLocales(locale)[0] ?? null;
  } catch {
    return null;
  }
}

// Direction follows the locale's script, not its language, so "pa-Arab" is
// RTL while "pa" is not. maximize() fills in the likely script for a bare
// language tag ("ar" → "ar-Arab-EG").
export function textDirection(locale: string): TextDirection {
  try {
    const { script } = new Intl.Locale(locale).maximize();
    return script !== undefined && RTL_SCRIPTS.has(script) ? "rtl" : "ltr";
  } catch {
    return "ltr";
  }
}

function readStoredLocale(): string | null {
  if (!hasDocument()) return null;
  const stored = window.localStorage.getItem(STORAGE_KEY);
  return stored ? canonicalize(stored) : null;
}

function applyToDocument(locale: string): void {
  if (!hasDocument()) return;
  document.documentElement.setAttribute("lang", locale);
  document.documentElement.setAttribute("dir", textDirection(locale));
}

export class LocaleStore {
  private locale: string = readStoredLocale() ?? DEFAULT_LOCALE;
  private readonly listeners = new Set<Listener>();

  constructor() {
    applyToDocument(this.locale);
  }

  getLocale = (): string => {
    return this.locale;
  };

  setLocale(locale: string): void {
    const canonical = canonicalize(locale);
    if (canonical === null) throw new RangeError(`invalid BCP 47 locale: ${JSON.stringify(locale)}`);
    this.locale = canonical;
    applyToDocument(canonical);
    if (hasDocument()) window.localStorage.setItem(STORAGE_KEY, canonical);
    for (const listener of this.listeners) listener(canonical);
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const localeStore = new LocaleStore();

export interface UseLocaleResult {
  locale: string;
  direction: TextDirection;
  timezone: string;
  currency: string;
  dateFormat: Intl.DateTimeFormat;
  numberFormat: NumberFormatter;
  firstDayOfWeek: FirstDayOfWeek;
}

export type LocaleStoreLike = Pick<LocaleStore, "getLocale" | "subscribe">;

export function useLocale(
  store: LocaleStoreLike = localeStore,
  tenantFormat: TenantFormatStoreLike = tenantFormatStore,
  datePreferences: DatePreferencesStoreLike = datePreferencesStore,
): UseLocaleResult {
  const locale = useSyncExternalStore(store.subscribe, store.getLocale);
  const { numberFormat, firstDayOfWeek } = useTenantFormat(tenantFormat);
  const preferences = useSyncExternalStore(datePreferences.subscribe, datePreferences.get);
  const dateFormat = useMemo(() => createDateFormatter(locale, preferences), [locale, preferences]);
  const formatter = useMemo(() => createNumberFormatter(locale, {}, numberFormat), [locale, numberFormat]);
  return {
    locale,
    direction: textDirection(locale),
    numberFormat: formatter,
    firstDayOfWeek,
    timezone: preferences.timezone,
    currency: preferences.currency,
    dateFormat,
  };
}
