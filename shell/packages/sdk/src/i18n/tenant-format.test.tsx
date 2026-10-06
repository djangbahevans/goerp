import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { formatCurrency, formatNumber, formatPercent } from "./format.js";
import {
  createNumberFormatter,
  DEFAULT_TENANT_FORMAT,
  TenantFormatStore,
  tenantFormatStore,
  useTenantFormat,
} from "./tenant-format.js";
import { LocaleStore, useLocale } from "./use-locale.js";

afterEach(() => {
  cleanup();
  tenantFormatStore.set(DEFAULT_TENANT_FORMAT);
});

describe("createNumberFormatter", () => {
  it.each([
    ["1,234.56", "1,234.56"],
    ["1.234,56", "1.234,56"],
    ["1 234,56", "1 234,56"],
  ] as const)("writes 1234.56 as %s", (pattern, expected) => {
    expect(createNumberFormatter("en", { minimumFractionDigits: 2 }, pattern).format(1234.56)).toBe(expected);
  });

  it("groups every thousand of a large number and leaves an integer without a decimal part", () => {
    expect(createNumberFormatter("en", {}, "1.234,56").format(1234567)).toBe("1.234.567");
  });

  it("keeps the user's locale for digits and currency placement, replacing only the separators", () => {
    const arabic = createNumberFormatter("ar-EG", {}, "1,234.56").format(1234.5);
    expect(arabic).toBe("١,٢٣٤.٥");
    expect(createNumberFormatter("fr", { style: "currency", currency: "GHS" }, "1.234,56").format(1234.5)).toBe(
      new Intl.NumberFormat("fr", { style: "currency", currency: "GHS" })
        .formatToParts(1234.5)
        .map((part) => (part.type === "group" ? "." : part.value))
        .join(""),
    );
  });

  it("exposes the parts and resolved options of the underlying formatter", () => {
    const formatter = createNumberFormatter("en", { maximumFractionDigits: 1 }, "1.234,56");
    expect(formatter.formatToParts(1234.5).find((part) => part.type === "decimal")?.value).toBe(",");
    expect(formatter.resolvedOptions().maximumFractionDigits).toBe(1);
  });
});

describe("formatNumber, formatPercent and formatCurrency", () => {
  it("follow the tenant's number format", () => {
    tenantFormatStore.set({ numberFormat: "1.234,56", firstDayOfWeek: "monday" });

    expect(formatNumber(1234.56)).toBe("1.234,56");
    expect(formatPercent(0.1234)).toBe(
      new Intl.NumberFormat("en", { style: "percent", maximumFractionDigits: 2 }).format(0.1234).replace(".", ","),
    );
    expect(formatCurrency(123456, "GHS")).toBe("GHS\u00a01.234,56");
  });

  it("converts currency from minor units by the currency's own decimal places", () => {
    expect(formatCurrency(10000, "GHS")).toBe("GHS\u00a0100.00");
    expect(formatCurrency(10000, "XOF")).toBe(
      new Intl.NumberFormat("en", { style: "currency", currency: "XOF" }).format(10000),
    );
  });

  it("takes an explicit locale, keeping its digits", () => {
    expect(formatNumber(1234.56, { locale: "ar-EG" })).toBe("١,٢٣٤.٥٦");
  });
});

describe("TenantFormatStore", () => {
  it("notifies subscribers only when the format changes", () => {
    const store = new TenantFormatStore();
    let notified = 0;
    store.subscribe(() => {
      notified += 1;
    });

    store.set(DEFAULT_TENANT_FORMAT);
    store.set({ numberFormat: "1 234,56", firstDayOfWeek: "sunday" });

    expect(notified).toBe(1);
    expect(store.get()).toEqual({ numberFormat: "1 234,56", firstDayOfWeek: "sunday" });
  });

  it("re-renders a hook reading it", () => {
    const store = new TenantFormatStore();
    const { result } = renderHook(() => useTenantFormat(store));
    expect(result.current.firstDayOfWeek).toBe("monday");

    act(() => store.set({ numberFormat: "1,234.56", firstDayOfWeek: "sunday" }));

    expect(result.current.firstDayOfWeek).toBe("sunday");
  });
});

describe("useLocale", () => {
  it("reports the tenant's first day of week and a number formatter using its separators", () => {
    const tenantFormat = new TenantFormatStore();
    const { result } = renderHook(() => useLocale(new LocaleStore(), tenantFormat));
    expect(result.current.numberFormat.format(1234.5)).toBe("1,234.5");
    expect(result.current.firstDayOfWeek).toBe("monday");

    act(() => tenantFormat.set({ numberFormat: "1.234,56", firstDayOfWeek: "sunday" }));

    expect(result.current.numberFormat.format(1234.5)).toBe("1.234,5");
    expect(result.current.firstDayOfWeek).toBe("sunday");
  });
});
