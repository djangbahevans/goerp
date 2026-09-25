import { useCallback, useMemo, useSyncExternalStore } from "react";
import { type TranslationParams, type TranslationStore, translationStore } from "./translation-store.js";
import { type LocaleStoreLike, localeStore } from "./use-locale.js";

export type TranslateFn = (key: string, params?: TranslationParams) => string;

export interface UseTranslationResult {
  t: TranslateFn;
}

// typescript-sdk-reference.md §10 "useTranslation": t('category.key') looks up
// '{moduleName}.category.key' through the locale fallback chain and returns
// the key itself when nothing matches.
export function useTranslation(
  moduleName: string,
  store: Pick<TranslationStore, "getVersion" | "subscribe" | "translate"> = translationStore,
  locales: LocaleStoreLike = localeStore,
): UseTranslationResult {
  const locale = useSyncExternalStore(locales.subscribe, locales.getLocale);
  const version = useSyncExternalStore(store.subscribe, () => store.getVersion(moduleName));

  // biome-ignore lint/correctness/useExhaustiveDependencies: version isn't read here; it changes t's identity when the module's translations load.
  const t = useCallback<TranslateFn>(
    (key, params) => store.translate(moduleName, locale, key, params),
    [store, moduleName, locale, version],
  );
  return useMemo(() => ({ t }), [t]);
}
