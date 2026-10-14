import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DatePreferencesStore, DEFAULT_DATE_PREFERENCES, datePreferencesStore } from "./date-format.js";
import { formatDate, formatDateTime, formatRelativeTime } from "./index.js";
import { TenantFormatStore } from "./tenant-format.js";
import { LocaleStore, localeStore, useLocale } from "./use-locale.js";

const date = new Date("2026-05-08T10:22:00Z");

beforeEach(() => {
  localeStore.setLocale("en");
  datePreferencesStore.set(DEFAULT_DATE_PREFERENCES);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  localeStore.setLocale("en");
  datePreferencesStore.set(DEFAULT_DATE_PREFERENCES);
});

describe("date formatting", () => {
  it.each(["short", "medium"] as const)("matches Intl's %s style for inherited date order", (dateStyle) => {
    for (const locale of ["en", "fr", "ar-EG"]) {
      localeStore.setLocale(locale);
      expect(formatDate(date, dateStyle)).toBe(
        new Intl.DateTimeFormat(locale, { dateStyle, timeZone: "UTC" }).format(date),
      );
    }
  });

  it("formats localized CLDR fields, literals and apostrophes in the user's timezone", () => {
    expect(formatDate(date, "dd MMM yyyy")).toBe("08 May 2026");
    expect(formatDate(date, "yyyy-MM-dd 'at home' ''")).toBe("2026-05-08 at home '");
    localeStore.setLocale("fr");
    expect(formatDate(date, "EEEE d MMMM yyyy")).toBe("vendredi 8 mai 2026");
    localeStore.setLocale("ar-EG");
    expect(formatDate(date, "dd/MM/yyyy")).toBe("٠٨/٠٥/٢٠٢٦");
    datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, timezone: "America/Los_Angeles" });
    expect(formatDate(new Date("2026-05-08T01:00:00Z"), "yyyy-MM-dd")).toBe("٢٠٢٦-٠٥-٠٧");
  });

  it.each([
    ["day_first", "8 May 2026", "08/05/2026"],
    ["month_first", "May 8, 2026", "05/08/2026"],
    ["iso", "2026-05-08", "2026-05-08"],
  ] as const)("honours %s without overriding an explicit pattern", (dateOrder, medium, short) => {
    datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, dateOrder });
    expect(formatDate(date)).toBe(medium);
    expect(formatDate(date, "short")).toBe(short);
    expect(formatDate(date, "dd MMM yyyy")).toBe("08 May 2026");
    expect(formatDateTime(date)).toBe(`${medium}, 10:22 AM`);
  });

  it("formats date and time in the current locale and timezone across a DST change", () => {
    datePreferencesStore.set({ ...DEFAULT_DATE_PREFERENCES, timezone: "America/New_York" });
    for (const value of [new Date("2026-03-08T06:30:00Z"), new Date("2026-03-08T07:30:00Z")]) {
      expect(formatDateTime(value)).toBe(
        new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short", timeZone: "America/New_York" }).format(
          value,
        ),
      );
    }
  });

  it("rejects invalid dates and unsupported or unclosed patterns", () => {
    expect(() => formatDate(new Date(Number.NaN))).toThrow(RangeError);
    expect(() => formatDateTime(new Date(Number.NaN))).toThrow(RangeError);
    expect(() => formatDate(date, "QQQ")).toThrow(RangeError);
    expect(() => formatDate(date, "dd 'month")).toThrow(RangeError);
  });
});

describe("relative time", () => {
  it("formats past and future instants in the user's locale", () => {
    vi.useFakeTimers();
    vi.setSystemTime(date);
    expect(formatRelativeTime(new Date(Date.now() - 86400000))).toBe("1 day ago");
    expect(formatRelativeTime(Date.now() + 3600000)).toBe("in 1 hour");
    expect(formatRelativeTime(Date.now() + 3600000 - 1)).toBe("in 1 hour");
    expect(formatRelativeTime(Date.now() - 3600000 + 1)).toBe("1 hour ago");
    localeStore.setLocale("fr");
    expect(formatRelativeTime(Date.now() - 86400000)).toBe(
      new Intl.RelativeTimeFormat("fr", { numeric: "always" }).format(-1, "day"),
    );
    expect(() => formatRelativeTime(new Date(Number.NaN))).toThrow(RangeError);
  });
});

describe("useLocale date preferences", () => {
  it("updates timezone, currency, date order and locale for all subscribed consumers", () => {
    const preferences = new DatePreferencesStore();
    const locale = new LocaleStore();
    locale.setLocale("en");
    const tenant = new TenantFormatStore();
    const { result } = renderHook(() => useLocale(locale, tenant, preferences));
    expect(result.current).toMatchObject({ timezone: "UTC", currency: "" });
    expect(result.current.dateFormat).toBeInstanceOf(Intl.DateTimeFormat);

    act(() => preferences.set({ timezone: "America/Los_Angeles", currency: "GHS", dateOrder: "day_first" }));
    expect(result.current).toMatchObject({ timezone: "America/Los_Angeles", currency: "GHS" });
    const boundary = new Date("2026-05-08T01:00:00Z");
    expect(result.current.dateFormat.format(boundary)).toBe("7 May 2026");
    expect(
      result.current.dateFormat
        .formatToParts(boundary)
        .map((part) => part.value)
        .join(""),
    ).toBe("7 May 2026");
    expect(result.current.dateFormat.resolvedOptions().timeZone).toBe("America/Los_Angeles");
    const detachedFormat = result.current.dateFormat.format;
    expect(detachedFormat(boundary)).toBe("7 May 2026");
    expect(result.current.dateFormat.formatRange(boundary, date)).toBe("7 May 2026 – 8 May 2026");
    expect(() => result.current.dateFormat.formatRange(date, boundary)).toThrow(RangeError);

    act(() => locale.setLocale("fr"));
    expect(result.current.dateFormat.format(boundary)).toBe("7 mai 2026");
  });
});
