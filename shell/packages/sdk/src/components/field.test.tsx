import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Field, formatFieldValue } from "./field.js";

afterEach(cleanup);

describe("formatFieldValue", () => {
  // l10n-guide.md: monetary amounts are always integer minor units (GHS
  // pesewas here) — 10000 minor units is GHS 100.00, not GHS 10,000.00.
  it("formats currency from integer minor units, converting by the currency's own decimal places", () => {
    expect(formatFieldValue(10000, "currency", "GHS", "-")).toBe(
      new Intl.NumberFormat(undefined, { style: "currency", currency: "GHS" }).format(100),
    );
  });

  it("formats a zero-decimal currency (e.g. XOF) without dividing", () => {
    expect(formatFieldValue(10000, "currency", "XOF", "-")).toBe(
      new Intl.NumberFormat(undefined, { style: "currency", currency: "XOF" }).format(10000),
    );
  });

  it("formats currency as a plain number when no currency code is given", () => {
    expect(formatFieldValue(10000, "currency", undefined, "-")).toBe(new Intl.NumberFormat().format(10000));
  });

  it("formats percent from a 0-1 decimal", () => {
    expect(formatFieldValue(0.5, "percent", undefined, "-")).toBe(
      new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 2 }).format(0.5),
    );
  });

  it("formats boolean as Yes/No", () => {
    expect(formatFieldValue(true, "boolean", undefined, "-")).toBe("Yes");
    expect(formatFieldValue(false, "boolean", undefined, "-")).toBe("No");
  });

  it("formats a date-like string with dateStyle medium", () => {
    expect(formatFieldValue("2026-03-05", "date", undefined, "-")).toBe(
      new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date("2026-03-05")),
    );
  });

  it("falls back to emptyText for null/undefined/empty-string values", () => {
    expect(formatFieldValue(null, "text", undefined, "-")).toBe("-");
    expect(formatFieldValue(undefined, "text", undefined, "-")).toBe("-");
    expect(formatFieldValue("", "text", undefined, "-")).toBe("-");
  });

  it("falls back to emptyText for an unparseable date", () => {
    expect(formatFieldValue("not-a-date", "date", undefined, "-")).toBe("-");
  });

  it("passes text/email/phone/url through as a plain string", () => {
    expect(formatFieldValue("a@b.com", "email", undefined, "-")).toBe("a@b.com");
  });
});

describe("Field", () => {
  it("renders the formatted value", () => {
    const { container } = render(<Field label="Amount" value={10000} type="currency" currency="GHS" />);
    const formatted = new Intl.NumberFormat(undefined, { style: "currency", currency: "GHS" }).format(100);
    // Intl may separate the currency code/symbol from the amount with a
    // non-breaking space, which getByText's whitespace normalizer collapses
    // differently on either side of an exact-string match — compare
    // normalized text instead of searching the DOM for the raw Intl output.
    expect(container.textContent?.replace(/\s+/g, " ")).toContain(formatted.replace(/\s+/g, " "));
  });

  it("renders no label element when label is omitted", () => {
    render(<Field value="a@b.com" type="email" />);
    expect(screen.getByText("a@b.com")).toBeTruthy();
  });
});
