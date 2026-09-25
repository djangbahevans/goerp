import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import { localeChain, type TranslationMessages, type TranslationStore, translationStore } from "./translation-store.js";
import { type LocaleStoreLike, localeStore } from "./use-locale.js";

// Fetches a module's frontend translations for every step of the current
// locale's fallback chain (l10n-guide.md §7 "Translation loading"), and the
// missing files for every loaded module when the locale changes.
export class TranslationLoader {
  private readonly modules = new Set<string>();
  private readonly requests = new Map<string, Promise<void>>();
  private readonly generations = new Map<string, number>();
  private watchingLocale = false;

  constructor(
    private readonly store: Pick<TranslationStore, "set"> = translationStore,
    private readonly locales: LocaleStoreLike = localeStore,
    private readonly client: Pick<APIClient, "get"> = apiClient,
  ) {}

  // Resolves once every file in the chain has loaded or failed; a failure
  // leaves t() falling back down the chain rather than blocking a render.
  load(moduleName: string): Promise<void> {
    if (!this.watchingLocale) {
      this.watchingLocale = true;
      this.locales.subscribe((locale) => {
        for (const name of this.modules) void this.loadChain(name, locale);
      });
    }
    this.modules.add(moduleName);
    return this.loadChain(moduleName, this.locales.getLocale());
  }

  // Refetches every loaded module's files, for a hot reload that may have
  // changed them; a response to a request made before this is discarded.
  async refreshLoaded(): Promise<void> {
    this.requests.clear();
    const locale = this.locales.getLocale();
    await Promise.all(
      [...this.modules].map((name) => {
        this.generations.set(name, this.generation(name) + 1);
        return this.loadChain(name, locale);
      }),
    );
  }

  private generation(moduleName: string): number {
    return this.generations.get(moduleName) ?? 0;
  }

  private async loadChain(moduleName: string, locale: string): Promise<void> {
    await Promise.all(localeChain(locale).map((l) => this.loadLocale(moduleName, l)));
  }

  private loadLocale(moduleName: string, locale: string): Promise<void> {
    const key = `${moduleName}\u0000${locale}`;
    const existing = this.requests.get(key);
    if (existing) return existing;

    const generation = this.generation(moduleName);
    const path = `/modules/${encodeURIComponent(moduleName)}/translations/${encodeURIComponent(locale)}.json`;
    const request = this.client.get<TranslationMessages>(path).then(
      (messages) => {
        // A refresh started since this request; its own request wins.
        if (this.generation(moduleName) === generation) this.store.set(moduleName, locale, messages);
      },
      (err: unknown) => {
        if (err instanceof AppError && err.isNotFound()) {
          if (this.generation(moduleName) === generation) this.store.set(moduleName, locale, {});
          return;
        }
        // Forget the failure so the next load or locale change retries it.
        if (this.requests.get(key) === request) this.requests.delete(key);
        console.warn(`load ${moduleName} translations for ${locale}:`, err);
      },
    );
    this.requests.set(key, request);
    return request;
  }
}

export const translationLoader = new TranslationLoader();
