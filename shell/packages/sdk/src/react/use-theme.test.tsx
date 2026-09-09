import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { themeStore, useTheme } from "./use-theme.js";

// A fresh fake store per test — the real module-level themeStore is a
// singleton, so testing against it directly would leak state (and the
// document-theme side effect) across tests.
function fakeStore(initial: "light" | "dark" = "light") {
  let theme = initial;
  const listeners = new Set<(t: "light" | "dark") => void>();
  return {
    getTheme: () => theme,
    toggleTheme: () => {
      theme = theme === "dark" ? "light" : "dark";
      for (const l of listeners) l(theme);
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
});
