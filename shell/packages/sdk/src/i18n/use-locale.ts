import { useSyncExternalStore } from "react";

export type TextDirection = "ltr" | "rtl";

// l10n-guide.md §2's platform default, the last step of the
// user → tenant → platform resolution chain.
export const DEFAULT_LOCALE = "en";

// shell-architecture.md §5: LocaleProvider's value is read from
// localStorage synchronously, so the right direction applies before the
// first render. Same key convention as use-theme.ts's "goerp-theme".
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

// Module-level singleton, same shape as use-theme.ts's ThemeStore. l10n-guide
// §9 has the shell set lang/dir on <html>, which this does on construction
// and on every change.
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
}

export type LocaleStoreLike = Pick<LocaleStore, "getLocale" | "subscribe">;

export function useLocale(store: LocaleStoreLike = localeStore): UseLocaleResult {
  const locale = useSyncExternalStore(store.subscribe, store.getLocale);
  return { locale, direction: textDirection(locale) };
}
