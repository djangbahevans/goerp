import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_DATE_PREFERENCES, datePreferencesStore } from "../i18n/date-format.js";
import { interpolate, interpolatePath } from "./interpolate.js";

const record = { id: "01j", amount_due: 5000, empty: "", customer: { phone: "0244123456", id: null } };

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  datePreferencesStore.set(DEFAULT_DATE_PREFERENCES);
});

describe("interpolate", () => {
  it("substitutes top-level and nested record fields", () => {
    expect(interpolate("{record.amount_due}", record)).toBe("5000");
    expect(interpolate("Call {record.customer.phone}", record)).toBe("Call 0244123456");
  });

  it("resolves a missing field, a null field, a path through a scalar and a missing record to an empty string", () => {
    expect(interpolate("{record.missing}", record)).toBe("");
    expect(interpolate("{record.customer.id}", record)).toBe("");
    expect(interpolate("{record.amount_due.value}", record)).toBe("");
    expect(interpolate("a{record.id}b", null)).toBe("ab");
  });

  it("leaves an unrecognised placeholder as is", () => {
    expect(interpolate("{user.name} {record.id}", record)).toBe("{user.name} 01j");
  });

  describe("{today}", () => {
    beforeEach(() => {
      vi.useFakeTimers();
      // 23:30 UTC on 16 May is already 17 May east of UTC.
      vi.setSystemTime(new Date("2026-05-16T23:30:00Z"));
    });

    it("is the date in the user's resolved timezone", () => {
      datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, timezone: "Africa/Accra" });
      expect(interpolate("{today}", null)).toBe("2026-05-16");
      datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, timezone: "Asia/Tokyo" });
      expect(interpolate("{today}", null)).toBe("2026-05-17");
    });

    it("falls back to UTC for an unrecognised timezone instead of throwing", () => {
      datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, timezone: "Not/AZone" });
      expect(interpolate("{today}", null)).toBe("2026-05-16");
    });
  });
});

describe("interpolatePath", () => {
  it("substitutes record fields into the path", () => {
    expect(interpolatePath("/sales/invoices/{record.id}", record)).toBe("/sales/invoices/01j");
    expect(interpolatePath("/contacts/{record.customer.phone}/invoices", record)).toBe("/contacts/0244123456/invoices");
  });

  it.each([
    ["no record", "/sales/invoices/{record.id}", null, "/sales/invoices"],
    ["a missing key", "/sales/invoices/{record.missing}", record, "/sales/invoices"],
    ["an empty value", "/contacts/{record.empty}/invoices", record, "/contacts/invoices"],
    ["a path through a scalar", "/x/{record.amount_due.value}", record, "/x"],
  ])("drops the segment and warns for %s", (_, template, rec, expected) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(interpolatePath(template, rec)).toBe(expected);
    expect(warn).toHaveBeenCalledOnce();
  });

  it("keeps the root path when its only segment drops", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(interpolatePath("/{record.missing}", record)).toBe("/");
  });

  it("leaves {today} and other non-record placeholders as is", () => {
    expect(interpolatePath("/reports/{today}", record)).toBe("/reports/{today}");
  });
});
