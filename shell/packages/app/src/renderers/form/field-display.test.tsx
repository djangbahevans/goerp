import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FieldDisplay } from "./field-display.js";
import type { FormField } from "./form-view-types.js";

const { resolveResourceMock } = vi.hoisted(() => ({
  resolveResourceMock: vi.fn(async () => ({ getPath: "/contacts/{id}" })),
}));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, resourceRegistry: { resolve: resolveResourceMock } };
});

afterEach(() => {
  cleanup();
  resolveResourceMock.mockClear();
});

function show(field: FormField, record: Record<string, unknown>): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrap = (children: ReactNode) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  render(wrap(<FieldDisplay field={field} record={record} resource="contacts.contact" />));
}

describe("FieldDisplay", () => {
  it("shows an empty value as a dash", () => {
    show({ field: "name", type: "text" }, { name: "" });
    expect(screen.getByText("No value").className).toContain("sr-only");
    expect(screen.getByText("—").getAttribute("aria-hidden")).toBe("true");
  });

  it("keeps the line breaks of a text value", () => {
    show({ field: "notes", type: "textarea" }, { notes: "first\nsecond" });
    expect(screen.getByText(/first/).className).toContain("whitespace-pre-wrap");
  });

  it("shows email, phone and url values as links", () => {
    show({ field: "email", type: "email" }, { email: "ada@acme.test" });
    expect(screen.getByRole("link", { name: "ada@acme.test" }).getAttribute("href")).toBe("mailto:ada@acme.test");
    cleanup();

    show({ field: "phone", type: "phone" }, { phone: "0501360696" });
    expect(screen.getByRole("link", { name: "0501360696" }).getAttribute("href")).toBe("tel:0501360696");
    cleanup();

    show({ field: "website", type: "url" }, { website: "https://acme.test" });
    expect(screen.getByRole("link", { name: "https://acme.test" }).getAttribute("href")).toBe("https://acme.test");
  });

  it("shows a boolean as Yes or No, including false", () => {
    show({ field: "is_customer", type: "boolean" }, { is_customer: true });
    expect(screen.getByText("Yes")).toBeTruthy();
    cleanup();

    show({ field: "is_customer", type: "toggle" }, { is_customer: false });
    expect(screen.getByText("No")).toBeTruthy();
  });

  it("shows a select by its option label, falling back to the stored value", () => {
    const field: FormField = {
      field: "type",
      type: "select",
      options: [{ value: "company", label: "Company" }],
    };
    show(field, { type: "company" });
    expect(screen.getByText("Company")).toBeTruthy();
    cleanup();

    show(field, { type: "legacy" });
    expect(screen.getByText("legacy")).toBeTruthy();
  });

  it("formats currency (minor units) with its currency field, and numbers for the locale", () => {
    show(
      { field: "balance", type: "currency", currency_field: "currency_code" },
      { balance: 125050, currency_code: "USD" },
    );
    expect(
      screen.getByText(new Intl.NumberFormat(undefined, { style: "currency", currency: "USD" }).format(1250.5)),
    ).toBeTruthy();
    cleanup();

    show({ field: "employees", type: "integer" }, { employees: 12000 });
    expect(screen.getByText(new Intl.NumberFormat().format(12000))).toBeTruthy();
  });

  it("formats a date", () => {
    show({ field: "founded", type: "date" }, { founded: "2016-04-01T00:00:00Z" });
    expect(screen.getByText(/2016/)).toBeTruthy();
  });

  it("shows tags as badges", () => {
    show({ field: "tag_ids", type: "tags", resource: "contacts.tag" }, { tags: [{ id: "1", name: "wholesale" }] });
    expect(screen.getByText("wholesale")).toBeTruthy();
  });

  it("links a relation to its record", async () => {
    show(
      { field: "company_id", type: "relation", resource: "contacts.contact" },
      { company: { id: "01c", display_name: "Acme Ltd" } },
    );
    await waitFor(() =>
      expect(screen.getByRole("link", { name: "Acme Ltd" }).getAttribute("href")).toBe("/_m/contacts/01c"),
    );
  });

  it("shows a relation as plain text while its record view is unknown", () => {
    resolveResourceMock.mockResolvedValueOnce({ getPath: "" });
    show(
      { field: "company_id", type: "relation", resource: "contacts.contact" },
      { company: { id: "01c", display_name: "Acme Ltd" } },
    );
    expect(screen.getByText("Acme Ltd")).toBeTruthy();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("falls back to a disabled control for a type with no display of its own", () => {
    show({ field: "payload", type: "json" }, { payload: { a: 1 } });
    expect(document.querySelector("[contenteditable]")?.getAttribute("contenteditable") ?? "false").not.toBe("true");
  });
});
