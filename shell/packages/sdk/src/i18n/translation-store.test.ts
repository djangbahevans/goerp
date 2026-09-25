import { describe, expect, it, vi } from "vitest";
import { localeChain, TranslationStore } from "./translation-store.js";

// l10n-guide.md §3's en.json and fr.json examples, abridged.
function contactsStore(): TranslationStore {
  const store = new TranslationStore();
  store.set("contacts", "en", {
    "fields.email.label": "Email Address",
    "fields.email.help": "Where we send receipts",
    "errors.duplicate_email": "A contact with {email} already exists",
    "messages.merged": "Merged {count} contacts into {target_name}",
    "count.contacts_one": "1 contact",
    "count.contacts_other": "{count} contacts",
    "count.orders_zero": "No orders",
    "count.orders_one": "1 order",
    "count.orders_other": "{count} orders",
  });
  store.set("contacts", "fr", {
    "fields.email.label": "Adresse e-mail",
    "count.contacts_other": "{count} contacts (fr)",
  });
  return store;
}

describe("localeChain", () => {
  it.each([
    ["fr-GH", ["fr-GH", "fr", "en"]],
    ["fr", ["fr", "en"]],
    ["en-GB", ["en-GB", "en"]],
    ["en", ["en"]],
    ["zh-Hant-TW", ["zh-Hant-TW", "zh", "en"]],
  ])("%s → %j", (locale, chain) => {
    expect(localeChain(locale)).toEqual(chain);
  });
});

describe("TranslationStore.translate", () => {
  it("resolves a key relative to the module", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "en", "fields.email.label")).toBe("Email Address");
    expect(store.translate("contacts", "en", "fields.email.help")).toBe("Where we send receipts");
  });

  it("keeps each module's keys apart", () => {
    const store = contactsStore();
    store.set("sales", "en", { "fields.email.label": "Billing email" });
    expect(store.translate("sales", "en", "fields.email.label")).toBe("Billing email");
    expect(store.translate("contacts", "en", "fields.email.label")).toBe("Email Address");
  });

  it("interpolates {name} placeholders and leaves unknown ones as written", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "en", "errors.duplicate_email", { email: "test@example.com" })).toBe(
      "A contact with test@example.com already exists",
    );
    expect(store.translate("contacts", "en", "messages.merged", { count: 3, target_name: "Acme Corp" })).toBe(
      "Merged 3 contacts into Acme Corp",
    );
    expect(store.translate("contacts", "en", "errors.duplicate_email")).toBe("A contact with {email} already exists");
  });

  it("selects the plural form from count", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "en", "count.contacts", { count: 1 })).toBe("1 contact");
    expect(store.translate("contacts", "en", "count.contacts", { count: 5 })).toBe("5 contacts");
  });

  it("uses _zero for a count of 0 when the key declares it, and _other otherwise", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "en", "count.orders", { count: 0 })).toBe("No orders");
    expect(store.translate("contacts", "en", "count.contacts", { count: 0 })).toBe("0 contacts");
  });

  it("uses the locale's CLDR categories, such as Arabic's two, few and many", () => {
    const store = new TranslationStore();
    store.set("sales", "ar", {
      "count.orders_zero": "zero",
      "count.orders_one": "one",
      "count.orders_two": "two",
      "count.orders_few": "few {count}",
      "count.orders_many": "many {count}",
      "count.orders_other": "other {count}",
    });
    const t = (count: number) => store.translate("sales", "ar", "count.orders", { count });
    expect([t(0), t(1), t(2), t(3), t(11), t(100)]).toEqual(["zero", "one", "two", "few 3", "many 11", "other 100"]);
  });

  it("falls back through the locale chain to the key itself", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "fr-GH", "fields.email.label")).toBe("Adresse e-mail");
    expect(store.translate("contacts", "fr-GH", "fields.email.help")).toBe("Where we send receipts");
    expect(store.translate("contacts", "fr-GH", "fields.missing.label")).toBe("fields.missing.label");
    expect(store.translate("unloaded", "en", "fields.email.label")).toBe("fields.email.label");
  });

  it("prefers a plural form in the user's locale over a more exact one in the fallback", () => {
    const store = contactsStore();
    expect(store.translate("contacts", "fr", "count.contacts", { count: 1 })).toBe("1 contacts (fr)");
  });

  it("treats a non-numeric count as an ordinary param", () => {
    const store = new TranslationStore();
    store.set("m", "en", { total: "{count} total", total_other: "plural" });
    expect(store.translate("m", "en", "total", { count: "many" })).toBe("many total");
  });
});

describe("TranslationStore versions", () => {
  it("bumps only the updated module's version and notifies subscribers", () => {
    const store = new TranslationStore();
    const listener = vi.fn();
    store.subscribe(listener);
    store.set("contacts", "en", {});
    expect(store.getVersion("contacts")).toBe(1);
    expect(store.getVersion("sales")).toBe(0);
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
