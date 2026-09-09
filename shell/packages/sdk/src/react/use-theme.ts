import { useSyncExternalStore } from "react";

export type Theme = "light" | "dark";

// shell-architecture.md's dark-mode section: `localStorage.getItem('goerp-theme')`.
const STORAGE_KEY = "goerp-theme";
type Listener = (theme: Theme) => void;

function hasDocument(): boolean {
  return typeof document !== "undefined" && typeof window !== "undefined";
}

function prefersDark(): boolean {
  return hasDocument() && window.matchMedia?.("(prefers-color-scheme: dark)").matches === true;
}

function readStoredTheme(): Theme | null {
  if (!hasDocument()) return null;
  const stored = window.localStorage.getItem(STORAGE_KEY);
  return stored === "light" || stored === "dark" ? stored : null;
}

// Module-level singleton, same shape as ToastBus/WebSocketManager. Every
// browser-global access is guarded — constructing it (at react/index.ts's
// own module scope) must never throw for a consumer of an unrelated hook.
class ThemeStore {
  private theme: Theme = readStoredTheme() ?? (prefersDark() ? "dark" : "light");
  private readonly listeners = new Set<Listener>();

  constructor() {
    if (hasDocument()) document.documentElement.setAttribute("data-theme", this.theme);
  }

  getTheme = (): Theme => {
    return this.theme;
  };

  setTheme(theme: Theme): void {
    this.theme = theme;
    if (hasDocument()) {
      document.documentElement.setAttribute("data-theme", theme);
      window.localStorage.setItem(STORAGE_KEY, theme);
    }
    for (const listener of this.listeners) listener(theme);
  }

  toggleTheme(): void {
    this.setTheme(this.theme === "dark" ? "light" : "dark");
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
  toggleTheme: () => void;
}

type ThemeStoreLike = Pick<ThemeStore, "getTheme" | "toggleTheme" | "subscribe">;

// Matches auth-provider.tsx's useSyncExternalStore(authMachine.subscribe,
// authMachine.getState) — same store shape, avoids a useState+useEffect's
// stale-value gap between first paint and the subscribing effect.
export function useTheme(store: ThemeStoreLike = themeStore): UseThemeResult {
  const theme = useSyncExternalStore(store.subscribe, store.getTheme);
  return { theme, toggleTheme: () => store.toggleTheme() };
}
