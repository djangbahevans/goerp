import { useSyncExternalStore } from "react";

// The theme applied to the page.
export type Theme = "light" | "dark";
// What the user chose: a theme, or "system" to follow the OS
// prefers-color-scheme setting live (shell-ux.md §4.4).
export type ThemePreference = Theme | "system";

// shell-architecture.md's dark-mode section: `localStorage.getItem('goerp-theme')`.
// index.html's pre-paint script resolves anything but "light"/"dark" through
// prefers-color-scheme, so storing "system" needs no change there.
const STORAGE_KEY = "goerp-theme";
const DARK_QUERY = "(prefers-color-scheme: dark)";
type Listener = (theme: Theme) => void;

function hasDocument(): boolean {
  return typeof document !== "undefined" && typeof window !== "undefined";
}

function darkQuery(): MediaQueryList | null {
  return hasDocument() && typeof window.matchMedia === "function" ? window.matchMedia(DARK_QUERY) : null;
}

function readStoredPreference(): ThemePreference {
  if (!hasDocument()) return "system";
  const stored = window.localStorage.getItem(STORAGE_KEY);
  return stored === "light" || stored === "dark" ? stored : "system";
}

// Module-level singleton, same shape as ToastBus/WebSocketManager. Every
// browser-global access is guarded — constructing it (at react/index.ts's
// own module scope) must never throw for a consumer of an unrelated hook.
export class ThemeStore {
  private preference: ThemePreference = readStoredPreference();
  private theme: Theme = this.resolve();
  private readonly listeners = new Set<Listener>();

  constructor() {
    if (hasDocument()) document.documentElement.setAttribute("data-theme", this.theme);
    // Registered once for the store's lifetime; it only acts while the
    // preference is "system".
    darkQuery()?.addEventListener?.("change", () => {
      if (this.preference === "system") this.apply();
    });
  }

  private resolve(): Theme {
    if (this.preference !== "system") return this.preference;
    return darkQuery()?.matches === true ? "dark" : "light";
  }

  private apply(): void {
    this.theme = this.resolve();
    if (hasDocument()) document.documentElement.setAttribute("data-theme", this.theme);
    for (const listener of this.listeners) listener(this.theme);
  }

  getTheme = (): Theme => {
    return this.theme;
  };

  getPreference = (): ThemePreference => {
    return this.preference;
  };

  setPreference(preference: ThemePreference): void {
    this.preference = preference;
    if (hasDocument()) window.localStorage.setItem(STORAGE_KEY, preference);
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
}

type ThemeStoreLike = Pick<ThemeStore, "getTheme" | "getPreference" | "setPreference" | "toggleTheme" | "subscribe">;

// Matches auth-provider.tsx's useSyncExternalStore(authMachine.subscribe,
// authMachine.getState) — same store shape, avoids a useState+useEffect's
// stale-value gap between first paint and the subscribing effect. Every
// preference change also notifies, so both values stay current.
export function useTheme(store: ThemeStoreLike = themeStore): UseThemeResult {
  const theme = useSyncExternalStore(store.subscribe, store.getTheme);
  const preference = useSyncExternalStore(store.subscribe, store.getPreference);
  return {
    theme,
    preference,
    setPreference: (next) => store.setPreference(next),
    toggleTheme: () => store.toggleTheme(),
  };
}
