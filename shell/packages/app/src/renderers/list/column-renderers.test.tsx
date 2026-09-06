import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { columnStyle, renderCell, renderCellContent, renderHref } from "./column-renderers.js";
import type { ListColumn } from "./list-view-types.js";

afterEach(cleanup);

function cell(column: Partial<ListColumn> & { field: string }, row: Record<string, unknown>) {
  return render(<>{renderCellContent(column as ListColumn, row)}</>).container;
}

describe("renderHref", () => {
  it("substitutes {record.field} template variables", () => {
    expect(renderHref("/contacts/{record.id}", { id: "01j..." })).toBe("/contacts/01j...");
    expect(renderHref("/contacts/{record.id}/{record.slug}", { id: "1", slug: "ada" })).toBe("/contacts/1/ada");
  });

  it("substitutes a missing field as an empty string", () => {
    expect(renderHref("/contacts/{record.id}", {})).toBe("/contacts/");
  });
});

describe("columnStyle", () => {
  it("applies width/min_width/max_width/align, and truncates by default", () => {
    expect(columnStyle({ field: "x", width: 100, min_width: 50, max_width: 200, align: "right" })).toEqual({
      width: 100,
      minWidth: 50,
      maxWidth: 200,
      textAlign: "right",
      overflow: "hidden",
      textOverflow: "ellipsis",
      whiteSpace: "nowrap",
    });
  });

  it("omits truncation when truncate is explicitly false", () => {
    expect(columnStyle({ field: "x", truncate: false })).toEqual({});
  });
});

describe("renderCellContent", () => {
  it("text: renders the raw value, empty string for null/undefined", () => {
    expect(cell({ field: "name", type: "text" }, { name: "Ada" }).textContent).toBe("Ada");
    expect(cell({ field: "name", type: "text" }, { name: null }).textContent).toBe("");
  });

  it("number: locale-formats a numeric value", () => {
    expect(cell({ field: "n", type: "number" }, { n: 1234.5 }).textContent).toBe(
      new Intl.NumberFormat(undefined).format(1234.5),
    );
  });

  it("currency: uses currency_field to pick the currency code", () => {
    const html = cell(
      { field: "amount", type: "currency", currency_field: "currency" },
      { amount: 10, currency: "GHS" },
    );
    expect(html.textContent).toBe(new Intl.NumberFormat(undefined, { style: "currency", currency: "GHS" }).format(10));
  });

  it("currency: falls back to plain number formatting instead of throwing when currency_field holds an invalid code", () => {
    const html = cell(
      { field: "amount", type: "currency", currency_field: "currency" },
      { amount: 10, currency: "Not A Code" },
    );
    expect(html.textContent).toBe(new Intl.NumberFormat(undefined).format(10));
  });

  it("currency: falls back to plain number formatting with no currency_field value", () => {
    expect(cell({ field: "amount", type: "currency" }, { amount: 10 }).textContent).toBe(
      new Intl.NumberFormat(undefined).format(10),
    );
  });

  it("percent: formats a fraction as a percentage", () => {
    expect(cell({ field: "rate", type: "percent" }, { rate: 0.42 }).textContent).toBe(
      new Intl.NumberFormat(undefined, { style: "percent" }).format(0.42),
    );
  });

  it("date/datetime/time: applies a CLDR format pattern when given", () => {
    const row = { d: "2026-03-05T14:30:00Z" };
    const html = cell({ field: "d", type: "date", format: "dd MMM yyyy" }, row);
    const date = new Date(row.d);
    const expected = `${String(date.getDate()).padStart(2, "0")} ${date.toLocaleString(undefined, { month: "short" })} ${date.getFullYear()}`;
    expect(html.textContent).toBe(expected);
  });

  it("date: falls back to locale formatting with no format pattern", () => {
    const row = { d: "2026-03-05T14:30:00Z" };
    expect(cell({ field: "d", type: "date" }, row).textContent).toBe(
      new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(row.d)),
    );
  });

  it("date: renders nothing for an unparseable value", () => {
    expect(cell({ field: "d", type: "date" }, { d: "not-a-date" }).textContent).toBe("");
  });

  it("relative_time: renders via Intl.RelativeTimeFormat", () => {
    const oneHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
    const html = cell({ field: "d", type: "relative_time" }, { d: oneHourAgo });
    expect(html.textContent).toBe(new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(-1, "hours"));
  });

  it("boolean: renders a check or cross with an accessible label", () => {
    expect(
      cell({ field: "active", type: "boolean" }, { active: true }).querySelector("[aria-label='Yes']"),
    ).not.toBeNull();
    expect(
      cell({ field: "active", type: "boolean" }, { active: false }).querySelector("[aria-label='No']"),
    ).not.toBeNull();
  });

  it("badge: looks up badge_config by the field's value and applies its color", () => {
    const column = {
      field: "state",
      type: "badge" as const,
      badge_config: { draft: { label: "Draft", color: "gray" }, done: { label: "Done", color: "green" } },
    };
    const html = cell(column, { state: "done" });
    expect(html.textContent).toBe("Done");
    expect(html.querySelector("span")?.className).toContain("bg-green-100");
  });

  it("badge: falls back to the raw value when it has no badge_config entry", () => {
    const column = { field: "state", type: "badge" as const, badge_config: {} };
    expect(cell(column, { state: "unknown" }).textContent).toBe("unknown");
  });

  it("avatar: renders an <img> when a file URL is present, initials otherwise", () => {
    const withImage = cell(
      { field: "name", type: "avatar", avatar_field: "avatar" },
      { name: "Ada Lovelace", avatar: { url: "https://example.com/a.png" } },
    );
    expect(withImage.querySelector("img")?.getAttribute("src")).toBe("https://example.com/a.png");

    const withoutImage = cell({ field: "name", type: "avatar", avatar_field: "avatar" }, { name: "Ada Lovelace" });
    expect(withoutImage.textContent).toBe("AL");
    // Preserves the original inline implementation's fixed h-8 w-8 size.
    expect(withoutImage.querySelector("span")?.className).toContain("h-8");
  });

  it("email/phone/url: render the expected link", () => {
    expect(cell({ field: "e", type: "email" }, { e: "a@b.com" }).querySelector("a")?.getAttribute("href")).toBe(
      "mailto:a@b.com",
    );
    expect(cell({ field: "p", type: "phone" }, { p: "+233" }).querySelector("a")?.getAttribute("href")).toBe(
      "tel:+233",
    );
    expect(cell({ field: "u", type: "url" }, { u: "https://x.com" }).querySelector("a")?.getAttribute("href")).toBe(
      "https://x.com",
    );
  });

  it("country: renders a flag and the display name", () => {
    const html = cell({ field: "c", type: "country" }, { c: "GH" });
    expect(html.textContent).toContain(new Intl.DisplayNames(undefined, { type: "region" }).of("GH"));
    expect(html.textContent).toContain("🇬🇭");
  });

  it("tags: renders one pill per array entry", () => {
    const html = cell({ field: "tags", type: "tags" }, { tags: ["vip", "eu"] });
    expect(html.textContent).toBe("vipeu");
  });

  it("relation: reads display_field directly when set", () => {
    const column = { field: "customer_id", type: "relation" as const, display_field: "customer_name" };
    expect(cell(column, { customer_id: "01j...", customer_name: "Acme Inc" }).textContent).toBe("Acme Inc");
  });

  it("relation: falls back to the batch-fetched relationLabel option when no display_field is set", () => {
    const column = { field: "customer_id", type: "relation" as const };
    const html = render(
      <>{renderCellContent(column, { customer_id: "01j..." }, { relationLabel: "Acme Inc" })}</>,
    ).container;
    expect(html.textContent).toBe("Acme Inc");
  });

  it("file: renders a download link with the file name", () => {
    const html = cell({ field: "doc", type: "file" }, { doc: { url: "https://x.com/f.pdf", name: "contract.pdf" } });
    const link = html.querySelector("a");
    expect(link?.getAttribute("href")).toBe("https://x.com/f.pdf");
    expect(link?.textContent).toBe("contract.pdf");
  });

  it("color: renders a swatch with the value as its background color", () => {
    const html = cell({ field: "c", type: "color" }, { c: "#ff0000" });
    const swatch = html.querySelector("[aria-label='#ff0000']") as HTMLElement | null;
    expect(swatch?.style.backgroundColor).toBe("rgb(255, 0, 0)");
  });

  it("json: renders a collapsible viewer with the value pretty-printed", () => {
    const html = cell({ field: "meta", type: "json" }, { meta: { a: 1 } });
    expect(html.querySelector("details")).not.toBeNull();
    expect(html.querySelector("pre")?.textContent).toBe(JSON.stringify({ a: 1 }, null, 2));
  });

  it("custom: falls back to the raw value — no module component registry exists yet", () => {
    expect(cell({ field: "x", type: "custom" }, { x: "raw" }).textContent).toBe("raw");
  });
});

describe("renderCell", () => {
  it("wraps the cell content in an <a> when href is declared, substituting record fields", () => {
    const html = render(
      <>{renderCell({ field: "name", href: "/contacts/{record.id}" } as ListColumn, { id: "1", name: "Ada" })}</>,
    ).container;
    const link = html.querySelector("a");
    expect(link?.getAttribute("href")).toBe("/contacts/1");
    expect(link?.textContent).toBe("Ada");
  });
});
