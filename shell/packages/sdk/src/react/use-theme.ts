import { useSyncExternalStore } from "react";

// The theme applied to the page.
export type Theme = "light" | "dark";
// What the user chose: a theme, or "system" to follow the OS
// prefers-color-scheme setting live (shell-ux.md §4.4).
export type ThemePreference = Theme | "system";
// The contrast level applied to the page — orthogonal to Theme, so light
// and dark each have a high-contrast variant (shell-visual-design.md §4).
export type Contrast = "standard" | "high";
// What the user chose: a level, or "system" to follow the OS
// prefers-contrast setting live (shell-ux.md §4.4).
export type ContrastPreference = Contrast | "system";

// shell-architecture.md's dark-mode section: `localStorage.getItem('goerp-theme')`.
// index.html's pre-paint script resolves anything but "light"/"dark" through
// prefers-color-scheme, so storing "system" needs no change there. Contrast
// follows the same shape under its own key.
const STORAGE_KEY = "goerp-theme";
const CONTRAST_STORAGE_KEY = "goerp-contrast";
const DARK_QUERY = "(prefers-color-scheme: dark)";
const HIGH_CONTRAST_QUERY = "(prefers-contrast: more)";
type Listener = (theme: Theme) => void;

function hasDocument(): boolean {
  return typeof document !== "undefined" && typeof window !== "undefined";
}

function mediaQuery(query: string): MediaQueryList | null {
  return hasDocument() && typeof window.matchMedia === "function" ? window.matchMedia(query) : null;
}

function readStoredPreference(): ThemePreference {
  if (!hasDocument()) return "system";
  const stored = window.localStorage.getItem(STORAGE_KEY);
  return stored === "light" || stored === "dark" ? stored : "system";
}

function readStoredContrastPreference(): ContrastPreference {
  if (!hasDocument()) return "system";
  const stored = window.localStorage.getItem(CONTRAST_STORAGE_KEY);
  return stored === "standard" || stored === "high" ? stored : "system";
}

// Module-level singleton, same shape as ToastBus/WebSocketManager. Every
// browser-global access is guarded — constructing it (at react/index.ts's
// own module scope) must never throw for a consumer of an unrelated hook.
export class ThemeStore {
  private preference: ThemePreference = readStoredPreference();
  private contrastPreference: ContrastPreference = readStoredContrastPreference();
  private theme: Theme = this.resolve();
  private contrast: Contrast = this.resolveContrast();
  private readonly listeners = new Set<Listener>();

  constructor() {
    this.writeAttributes();
    // Registered once for the store's lifetime; each only acts while its
    // preference is "system".
    mediaQuery(DARK_QUERY)?.addEventListener?.("change", () => {
      if (this.preference === "system") this.apply();
    });
    mediaQuery(HIGH_CONTRAST_QUERY)?.addEventListener?.("change", () => {
      if (this.contrastPreference === "system") this.apply();
    });
  }

  private resolve(): Theme {
    if (this.preference !== "system") return this.preference;
    return mediaQuery(DARK_QUERY)?.matches === true ? "dark" : "light";
  }

  private resolveContrast(): Contrast {
    if (this.contrastPreference !== "system") return this.contrastPreference;
    return mediaQuery(HIGH_CONTRAST_QUERY)?.matches === true ? "high" : "standard";
  }

  private writeAttributes(): void {
    if (!hasDocument()) return;
    document.documentElement.setAttribute("data-theme", this.theme);
    document.documentElement.setAttribute("data-contrast", this.contrast);
  }

  private apply(): void {
    this.theme = this.resolve();
    this.contrast = this.resolveContrast();
    this.writeAttributes();
    for (const listener of this.listeners) listener(this.theme);
  }

  getTheme = (): Theme => {
    return this.theme;
  };

  getPreference = (): ThemePreference => {
    return this.preference;
  };

  getContrast = (): Contrast => {
    return this.contrast;
  };

  getContrastPreference = (): ContrastPreference => {
    return this.contrastPreference;
  };

  setPreference(preference: ThemePreference): void {
    this.preference = preference;
    if (hasDocument()) window.localStorage.setItem(STORAGE_KEY, preference);
    this.apply();
  }

  setContrastPreference(preference: ContrastPreference): void {
    this.contrastPreference = preference;
    if (hasDocument()) window.localStorage.setItem(CONTRAST_STORAGE_KEY, preference);
    this.apply();
  }

  setTheme(theme: Theme): void {
    this.setPreference(theme);
  }

  toggleTheme(): void {
    this.setPreference(this.theme === "dark" ? "light" : "dark");
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const themeStore = new ThemeStore();

export interface UseThemeResult {
  theme: Theme;
  preference: ThemePreference;
  setPreference: (preference: ThemePreference) => void;
  toggleTheme: () => void;
  contrast: Contrast;
  contrastPreference: ContrastPreference;
  setContrastPreference: (preference: ContrastPreference) => void;
}

type ThemeStoreLike = Pick<
  ThemeStore,
  | "getTheme"
  | "getPreference"
  | "setPreference"
  | "toggleTheme"
  | "getContrast"
  | "getContrastPreference"
  | "setContrastPreference"
  | "subscribe"
>;

// Matches auth-provider.tsx's useSyncExternalStore(authMachine.subscribe,
// authMachine.getState) — same store shape, avoids a useState+useEffect's
// stale-value gap between first paint and the subscribing effect. Every
// preference change also notifies, so all four values stay current.
export function useTheme(store: ThemeStoreLike = themeStore): UseThemeResult {
  const theme = useSyncExternalStore(store.subscribe, store.getTheme);
  const preference = useSyncExternalStore(store.subscribe, store.getPreference);
  const contrast = useSyncExternalStore(store.subscribe, store.getContrast);
  const contrastPreference = useSyncExternalStore(store.subscribe, store.getContrastPreference);
  return {
    theme,
    preference,
    setPreference: (next) => store.setPreference(next),
    toggleTheme: () => store.toggleTheme(),
    contrast,
    contrastPreference,
    setContrastPreference: (next) => store.setContrastPreference(next),
  };
}
