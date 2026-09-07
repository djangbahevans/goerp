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

  it("formats a past date as a relative time", () => {
    expect(formatFieldValue(new Date(Date.now() - 60_000), "relative_time", undefined, "-")).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(-1, "minutes"),
    );
  });

  it("formats a raw epoch-ms number as a relative time, not just a Date/ISO string", () => {
    expect(formatFieldValue(Date.now() - 60_000, "relative_time", undefined, "-")).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(-1, "minutes"),
    );
  });

  it("joins tag names as plain comma-separated text", () => {
    expect(formatFieldValue([{ name: "VIP" }, { name: "Lead" }], "tags", undefined, "-")).toBe("VIP, Lead");
  });

  it("falls back to emptyText for an empty tags array", () => {
    expect(formatFieldValue([], "tags", undefined, "-")).toBe("-");
  });

  it("skips malformed tag entries with no string name, rather than printing 'undefined'", () => {
    expect(formatFieldValue([{ id: "1" }, { name: "VIP" }], "tags", undefined, "-")).toBe("VIP");
  });

  it("renders a relation value's display text", () => {
    expect(formatFieldValue({ id: "1", display: "Acme Corp" }, "relation", undefined, "-")).toBe("Acme Corp");
  });

  it("renders a file value's name, falling back to its id", () => {
    expect(formatFieldValue({ id: "f1", name: "invoice.pdf" }, "file", undefined, "-")).toBe("invoice.pdf");
    expect(formatFieldValue({ id: "f1" }, "file", undefined, "-")).toBe("f1");
    expect(formatFieldValue("plain-filename.pdf", "file", undefined, "-")).toBe("plain-filename.pdf");
  });

  it("stringifies a json value", () => {
    expect(formatFieldValue({ a: 1 }, "json", undefined, "-")).toBe('{"a":1}');
  });

  it("falls back to emptyText for a value JSON.stringify can't serialize, e.g. a circular reference", () => {
    // biome-ignore lint/suspicious/noExplicitAny: building a circular reference for the test needs a mutable any.
    const circular: any = { a: 1 };
    circular.self = circular;
    expect(formatFieldValue(circular, "json", undefined, "-")).toBe("-");
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

  it("renders the value as a link when href is given", () => {
    render(<Field label="Manager" value="Kwame Mensah" href="/contacts/1" />);
    const link = screen.getByRole("link", { name: "Kwame Mensah" });
    expect(link.getAttribute("href")).toBe("/contacts/1");
  });

  it("renders plain text when href is omitted", () => {
    render(<Field label="Manager" value="Kwame Mensah" />);
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("delegates type='badge' to Badge, resolved from badgeConfig by the raw value", () => {
    render(
      <Field
        label="Status"
        value="done"
        type="badge"
        badgeConfig={{ done: { label: "Done", color: "green" }, draft: { label: "Draft", color: "gray" } }}
      />,
    );
    expect(screen.getByText("Done")).toBeTruthy();
  });

  it("falls back to the raw value for type='badge' when badgeConfig has no matching entry", () => {
    render(<Field label="Status" value="unknown" type="badge" badgeConfig={{}} />);
    expect(screen.getByText("unknown")).toBeTruthy();
  });

  it("delegates type='avatar' to UserAvatar", () => {
    render(<Field label="Owner" value={{ name: "Ama Owusu" }} type="avatar" />);
    expect(screen.getByText("AO")).toBeTruthy();
  });

  it("delegates type='country' to CountryFlag with the country name shown", () => {
    render(<Field label="Country" value="GH" type="country" />);
    expect(screen.getByRole("img")).toBeTruthy();
  });

  it("renders a color swatch with an unconditional border beside the value text", () => {
    render(<Field label="Tag color" value="#ff0000" type="color" />);
    expect(screen.getByText("#ff0000")).toBeTruthy();
    const swatch = document.querySelector('[aria-hidden="true"]') as HTMLElement;
    expect(swatch.style.backgroundColor).toBe("rgb(255, 0, 0)");
    expect(swatch.className).toContain("border");
  });

  it("truncates a json value with an ellipsis and holds the full string in title", () => {
    render(<Field label="Metadata" value={{ a: 1, b: 2 }} type="json" />);
    const value = screen.getByTitle('{"a":1,"b":2}');
    expect(value.className).toContain("truncate");
  });

  it("applies the same font-mono/truncate treatment to a linked json value", () => {
    render(<Field label="Metadata" value={{ a: 1 }} type="json" href="/records/1" />);
    const link = screen.getByRole("link");
    expect(link.className).toContain("font-mono");
    expect(link.className).toContain("truncate");
  });
});
