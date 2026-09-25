import { DEFAULT_LOCALE } from "./use-locale.js";

export type TranslationMessages = Readonly<Record<string, string>>;

export type TranslationParams = Readonly<Record<string, unknown>>;

// l10n-guide.md §2 "Translation locale fallback": the exact locale, then its
// language, then the platform default. The key itself is the last step.
export function localeChain(locale: string): string[] {
  let language = locale;
  try {
    language = new Intl.Locale(locale).language;
  } catch {
    // An unparseable tag still gets its exact match and the default.
  }
  return [...new Set([locale, language, DEFAULT_LOCALE])];
}

const pluralRulesByLocale = new Map<string, Intl.PluralRules | null>();

function pluralCategory(locale: string, count: number): Intl.LDMLPluralRule | null {
  let rules = pluralRulesByLocale.get(locale);
  if (rules === undefined) {
    try {
      rules = new Intl.PluralRules(locale);
    } catch {
      rules = null;
    }
    pluralRulesByLocale.set(locale, rules);
  }
  return rules?.select(count) ?? null;
}

// l10n-guide.md §5: `_zero` when count is 0 and the key declares it, then the
// locale's CLDR category (`_one`, `_two`, `_few`, ...), then `_other`, then
// the bare key.
function pluralCandidates(locale: string, key: string, count: number): string[] {
  const candidates: string[] = [];
  if (count === 0) candidates.push(`${key}_zero`);
  const category = pluralCategory(locale, count);
  if (category !== null) candidates.push(`${key}_${category}`);
  candidates.push(`${key}_other`, key);
  return candidates;
}

function interpolate(template: string, params: TranslationParams | undefined): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (placeholder, name: string) =>
    Object.hasOwn(params, name) ? String(params[name]) : placeholder,
  );
}

// Each module's frontend translations, per locale, as the shell loaded them
// (l10n-guide.md §7 "Translation loading"). Keys are relative to the module.
export class TranslationStore {
  private readonly byModule = new Map<string, Map<string, TranslationMessages>>();
  private readonly versions = new Map<string, number>();
  private readonly listeners = new Set<() => void>();

  set(moduleName: string, locale: string, messages: TranslationMessages): void {
    let locales = this.byModule.get(moduleName);
    if (!locales) {
      locales = new Map();
      this.byModule.set(moduleName, locales);
    }
    locales.set(locale, messages);
    this.versions.set(moduleName, this.getVersion(moduleName) + 1);
    for (const listener of this.listeners) listener();
  }

  // Changes whenever one of the module's locales is set, so a hook can
  // re-render only for the module it reads.
  getVersion = (moduleName: string): number => this.versions.get(moduleName) ?? 0;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  translate(moduleName: string, locale: string, key: string, params?: TranslationParams): string {
    const locales = this.byModule.get(moduleName);
    const count = params?.count;
    for (const candidateLocale of localeChain(locale)) {
      const messages = locales?.get(candidateLocale);
      if (!messages) continue;
      const candidates = typeof count === "number" ? pluralCandidates(candidateLocale, key, count) : [key];
      for (const candidate of candidates) {
        const message = messages[candidate];
        if (message !== undefined) return interpolate(message, params);
      }
    }
    return key;
  }
}

export const translationStore = new TranslationStore();
