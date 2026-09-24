import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_LOCALE, LocaleStore, textDirection, useLocale } from "./use-locale.js";

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(cleanup);

describe("textDirection", () => {
  it.each(["ar", "he", "ur", "fa", "ps", "yi", "dv", "ckb", "ar-GH", "pa-Arab"])("%s is rtl", (locale) => {
    expect(textDirection(locale)).toBe("rtl");
  });

  it.each(["en", "fr-GH", "ha", "tw", "pa", "zh-Hant"])("%s is ltr", (locale) => {
    expect(textDirection(locale)).toBe("ltr");
  });

  it("treats an unparseable tag as ltr", () => {
    expect(textDirection("not a locale")).toBe("ltr");
  });
});

describe("LocaleStore", () => {
  it("defaults to the platform locale when nothing is stored", () => {
    expect(new LocaleStore().getLocale()).toBe(DEFAULT_LOCALE);
  });

  it("reads and canonicalizes a stored locale", () => {
    window.localStorage.setItem("goerp-locale", "fr-gh");
    expect(new LocaleStore().getLocale()).toBe("fr-GH");
  });

  it("falls back to the platform locale when the stored value is invalid", () => {
    window.localStorage.setItem("goerp-locale", "not a locale");
    expect(new LocaleStore().getLocale()).toBe(DEFAULT_LOCALE);
  });

  it("sets lang and dir on <html> on construction and on change", () => {
    const store = new LocaleStore();
    expect(document.documentElement.getAttribute("lang")).toBe("en");
    expect(document.documentElement.getAttribute("dir")).toBe("ltr");

    store.setLocale("ar");
    expect(document.documentElement.getAttribute("lang")).toBe("ar");
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
  });

  it("persists a new locale to localStorage", () => {
    new LocaleStore().setLocale("he");
    expect(window.localStorage.getItem("goerp-locale")).toBe("he");
  });

  it("rejects an invalid locale without changing state", () => {
    const store = new LocaleStore();
    expect(() => store.setLocale("not a locale")).toThrow(RangeError);
    expect(store.getLocale()).toBe(DEFAULT_LOCALE);
  });
});

describe("useLocale", () => {
  it("returns the store's locale and its direction", () => {
    window.localStorage.setItem("goerp-locale", "ur");
    const { result } = renderHook(() => useLocale(new LocaleStore()));
    expect(result.current).toEqual({ locale: "ur", direction: "rtl" });
  });

  it("re-renders every subscribed consumer when the locale changes", () => {
    const store = new LocaleStore();
    const a = renderHook(() => useLocale(store));
    const b = renderHook(() => useLocale(store));

    act(() => store.setLocale("ar"));

    expect(a.result.current).toEqual({ locale: "ar", direction: "rtl" });
    expect(b.result.current).toEqual({ locale: "ar", direction: "rtl" });
  });
});
