import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { type ContrastPreference, type ThemePreference, ThemeStore, themeStore, useTheme } from "./use-theme.js";

// A fresh fake store per test — the real module-level themeStore is a
// singleton, so testing against it directly would leak state (and the
// document-theme side effect) across tests.
function fakeStore(initial: "light" | "dark" = "light") {
  let theme = initial;
  let preference: ThemePreference = initial;
  let contrastPreference: ContrastPreference = "standard";
  const listeners = new Set<(t: "light" | "dark") => void>();
  const notify = () => {
    for (const l of listeners) l(theme);
  };
  const set = (next: ThemePreference) => {
    preference = next;
    theme = next === "system" ? "light" : next;
    notify();
  };
  return {
    getTheme: () => theme,
    getPreference: () => preference,
    setPreference: set,
    toggleTheme: () => set(theme === "dark" ? "light" : "dark"),
    getContrast: () => (contrastPreference === "high" ? ("high" as const) : ("standard" as const)),
    getContrastPreference: () => contrastPreference,
    setContrastPreference: (next: ContrastPreference) => {
      contrastPreference = next;
      notify();
    },
    subscribe: (listener: (t: "light" | "dark") => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

afterEach(cleanup);

describe("useTheme", () => {
  it("returns the store's current theme", () => {
    const store = fakeStore("dark");
    const { result } = renderHook(() => useTheme(store));
    expect(result.current.theme).toBe("dark");
  });

  it("toggling updates every hook instance subscribed to the same store", () => {
    const store = fakeStore("light");
    const a = renderHook(() => useTheme(store));
    const b = renderHook(() => useTheme(store));

    act(() => a.result.current.toggleTheme());

    expect(a.result.current.theme).toBe("dark");
    expect(b.result.current.theme).toBe("dark");
  });

  it("setting the contrast preference updates contrast without touching the theme", () => {
    const store = fakeStore("dark");
    const { result } = renderHook(() => useTheme(store));

    act(() => result.current.setContrastPreference("high"));

    expect(result.current.contrastPreference).toBe("high");
    expect(result.current.contrast).toBe("high");
    expect(result.current.theme).toBe("dark");
  });
});

describe("themeStore (real singleton)", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("setting the theme updates document.documentElement's data-theme attribute", () => {
    themeStore.setTheme("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    themeStore.setTheme("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
  });

  it("persists the theme to localStorage", () => {
    themeStore.setTheme("dark");
    expect(window.localStorage.getItem("goerp-theme")).toBe("dark");
  });

  it("setting the contrast updates data-contrast and persists it, independent of data-theme", () => {
    themeStore.setTheme("dark");
    themeStore.setContrastPreference("high");
    expect(document.documentElement.getAttribute("data-contrast")).toBe("high");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(window.localStorage.getItem("goerp-contrast")).toBe("high");
    themeStore.setContrastPreference("standard");
    expect(document.documentElement.getAttribute("data-contrast")).toBe("standard");
  });
});

describe("ThemeStore system preference", () => {
  let dark = false;
  let moreContrast = false;
  let changeListener: (() => void) | null = null;
  let contrastChangeListener: (() => void) | null = null;
  const originalMatchMedia = window.matchMedia;

  beforeEach(() => {
    window.localStorage.clear();
    dark = false;
    moreContrast = false;
    changeListener = null;
    contrastChangeListener = null;
    window.matchMedia = ((query: string) => {
      const isContrast = query.includes("prefers-contrast");
      return {
        get matches() {
          return isContrast ? moreContrast : query.includes("dark") && dark;
        },
        addEventListener: (_type: string, listener: () => void) => {
          if (isContrast) contrastChangeListener = listener;
          else changeListener = listener;
        },
      };
    }) as unknown as typeof window.matchMedia;
  });
  afterEach(() => {
    window.matchMedia = originalMatchMedia;
  });

  it("defaults to system when nothing is stored, resolving through prefers-color-scheme", () => {
    dark = true;
    const store = new ThemeStore();
    expect(store.getPreference()).toBe("system");
    expect(store.getTheme()).toBe("dark");
  });

  it("follows an OS change live while the preference is system, and stores system", () => {
    const store = new ThemeStore();
    store.setPreference("system");
    expect(window.localStorage.getItem("goerp-theme")).toBe("system");
    expect(store.getTheme()).toBe("light");

    dark = true;
    changeListener?.();

    expect(store.getTheme()).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
  });

  it("ignores OS changes while an explicit theme is chosen", () => {
    const store = new ThemeStore();
    store.setPreference("light");

    dark = true;
    changeListener?.();

    expect(store.getTheme()).toBe("light");
  });

  it("toggles from the resolved theme to an explicit one", () => {
    dark = true;
    const store = new ThemeStore();
    store.toggleTheme();
    expect(store.getPreference()).toBe("light");
  });

  it("defaults contrast to system when nothing is stored, resolving through prefers-contrast", () => {
    moreContrast = true;
    const store = new ThemeStore();
    expect(store.getContrastPreference()).toBe("system");
    expect(store.getContrast()).toBe("high");
    expect(document.documentElement.getAttribute("data-contrast")).toBe("high");
  });

  it("follows an OS contrast change live while the contrast preference is system", () => {
    const store = new ThemeStore();
    store.setContrastPreference("system");
    expect(window.localStorage.getItem("goerp-contrast")).toBe("system");
    expect(store.getContrast()).toBe("standard");

    moreContrast = true;
    contrastChangeListener?.();

    expect(store.getContrast()).toBe("high");
    expect(document.documentElement.getAttribute("data-contrast")).toBe("high");
  });

  it("ignores OS contrast changes while an explicit level is chosen", () => {
    const store = new ThemeStore();
    store.setContrastPreference("standard");

    moreContrast = true;
    contrastChangeListener?.();

    expect(store.getContrast()).toBe("standard");
  });
});
