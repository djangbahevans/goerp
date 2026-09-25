import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { TranslationStore } from "./translation-store.js";
import { LocaleStore } from "./use-locale.js";
import { useTranslation } from "./use-translation.js";

let store: TranslationStore;
let locales: LocaleStore;

beforeEach(() => {
  window.localStorage.clear();
  store = new TranslationStore();
  locales = new LocaleStore();
});

afterEach(cleanup);

describe("useTranslation", () => {
  it("returns the key until the module's translations load, then re-renders with them", () => {
    const { result } = renderHook(() => useTranslation("contacts", store, locales));
    expect(result.current.t("fields.email.label")).toBe("fields.email.label");

    act(() => store.set("contacts", "en", { "fields.email.label": "Email Address" }));

    expect(result.current.t("fields.email.label")).toBe("Email Address");
  });

  it("re-renders translated text when the user's language changes", () => {
    store.set("contacts", "en", { "actions.edit": "Edit" });
    store.set("contacts", "fr", { "actions.edit": "Modifier" });
    const { result } = renderHook(() => useTranslation("contacts", store, locales));
    expect(result.current.t("actions.edit")).toBe("Edit");

    act(() => locales.setLocale("fr-GH"));

    expect(result.current.t("actions.edit")).toBe("Modifier");
  });

  it("interpolates params and selects plurals", () => {
    store.set("contacts", "en", { "count.contacts_one": "1 contact", "count.contacts_other": "{count} contacts" });
    const { result } = renderHook(() => useTranslation("contacts", store, locales));
    expect(result.current.t("count.contacts", { count: 1 })).toBe("1 contact");
    expect(result.current.t("count.contacts", { count: 5 })).toBe("5 contacts");
  });

  it("keeps the same result across re-renders and other modules' loads", () => {
    const { result, rerender } = renderHook(() => useTranslation("contacts", store, locales));
    const first = result.current;

    rerender();
    act(() => store.set("sales", "en", { "actions.edit": "Edit" }));

    expect(result.current).toBe(first);
  });
});
