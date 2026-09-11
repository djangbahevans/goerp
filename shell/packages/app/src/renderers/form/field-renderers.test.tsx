import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

const { resolveResourceMock, resolveMetadataMock, getMock } = vi.hoisted(() => ({
  resolveResourceMock: vi.fn(async () => ({ listPath: "/tags" })),
  resolveMetadataMock: vi.fn(
    async (): Promise<{ listRoute: string; labelField: string; searchParam: string } | undefined> => ({
      listRoute: "GET /tags",
      labelField: "name",
      searchParam: "q",
    }),
  ),
  getMock: vi.fn(async () => ({
    data: [
      { id: "1", name: "VIP" },
      { id: "2", name: "Lead" },
    ],
    meta: { cursor: null, hasMore: false },
  })),
}));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return {
    ...actual,
    resourceRegistry: { resolve: resolveResourceMock },
    resourceMetadataRegistry: { resolve: resolveMetadataMock },
  };
});

afterEach(() => {
  cleanup();
  resolveResourceMock.mockClear();
  resolveMetadataMock.mockClear();
  getMock.mockClear();
});

// jsdom doesn't implement scrollIntoView (jsdom/jsdom#1695) — @goerp/sdk's
// Select (Radix-based for its single-select mode) calls it internally
// whenever the panel opens.
Element.prototype.scrollIntoView = vi.fn();

function renderField(field: FormField, value: unknown, record: Row = {}) {
  const onChange = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <FieldInput field={field} value={value} onChange={onChange} record={record} />
    </QueryClientProvider>,
  );
  return onChange;
}

describe("readFieldValue/writeFieldValue", () => {
  it("reads a tags field from the pluralized, _ids-stripped key and writes back the _ids key", () => {
    const field: FormField = { field: "tag_ids", type: "tags" };
    const record: Row = { tag_ids: ["should-be-ignored"], tags: [{ id: "1", name: "VIP" }] };

    expect(readFieldValue(field, record)).toEqual([{ id: "1", name: "VIP" }]);
    expect(writeFieldValue(field, ["1", "2"])).toEqual({ tag_ids: ["1", "2"] });
  });

  it("reads/writes every other field type under its own field name", () => {
    const field: FormField = { field: "email", type: "email" };
    expect(readFieldValue(field, { email: "a@b.com" })).toBe("a@b.com");
    expect(writeFieldValue(field, "a@b.com")).toEqual({ email: "a@b.com" });
  });

  it("date_range/address: passes the sub-field patch through as-is instead of nesting it under the field's own name", () => {
    expect(writeFieldValue({ field: "range", type: "date_range" }, { starts_on: "2026-02-01" })).toEqual({
      starts_on: "2026-02-01",
    });
    expect(writeFieldValue({ field: "billing", type: "address" }, { street: "123 Main St" })).toEqual({
      street: "123 Main St",
    });
  });

  it("relation: reads the embedded, _id-stripped companion object as a RelationValue", () => {
    const field: FormField = { field: "customer_id", type: "relation" };
    const record: Row = { customer_id: "01j8...", customer: { id: "01j8...", display_name: "Acme Corp" } };
    expect(readFieldValue(field, record)).toEqual({ id: "01j8...", display: "Acme Corp" });
  });

  it("relation: reads null when the embedded companion is absent", () => {
    const field: FormField = { field: "customer_id", type: "relation" };
    expect(readFieldValue(field, { customer_id: "01j8..." })).toBeNull();
  });

  it("many2many/user_select with multiple: reads the embedded, _ids-stripped companion array as RelationValue[]", () => {
    const field: FormField = { field: "tag_ids", type: "many2many", multiple: true };
    const record: Row = {
      tag_ids: ["1", "2"],
      tags: [
        { id: "1", display_name: "VIP" },
        { id: "2", display_name: "Lead" },
      ],
    };
    expect(readFieldValue(field, record)).toEqual([
      { id: "1", display: "VIP" },
      { id: "2", display: "Lead" },
    ]);
  });

  it("many2many reads as an array even without an explicit multiple:true — the type itself implies it", () => {
    const field: FormField = { field: "tag_ids", type: "many2many" };
    const record: Row = { tag_ids: ["1"], tags: [{ id: "1", display_name: "VIP" }] };
    expect(readFieldValue(field, record)).toEqual([{ id: "1", display: "VIP" }]);
  });

  it("relation: falls back to the id as display text when the companion's display_name isn't set", () => {
    const field: FormField = { field: "customer_id", type: "relation" };
    const record: Row = { customer_id: "01j8...", customer: { id: "01j8...", display_name: null } };
    expect(readFieldValue(field, record)).toEqual({ id: "01j8...", display: "01j8..." });
  });

  it("relation/many2many/user_select: writeFieldValue writes the raw id(s) under the field's own name (unchanged, default behavior)", () => {
    expect(writeFieldValue({ field: "customer_id", type: "relation" }, "01j8...")).toEqual({
      customer_id: "01j8...",
    });
    expect(writeFieldValue({ field: "tag_ids", type: "many2many" }, ["1", "2"])).toEqual({ tag_ids: ["1", "2"] });
  });

  it("file: reads the embedded, _id-stripped companion object as a FileValue", () => {
    const field: FormField = { field: "signed_pdf_id", type: "file" };
    const record: Row = {
      signed_pdf_id: "01j...",
      signed_pdf: { id: "01j...", name: "Contract.pdf", content_type: "application/pdf", size_bytes: 245760 },
    };
    expect(readFieldValue(field, record)).toEqual({
      fileId: "01j...",
      name: "Contract.pdf",
      contentType: "application/pdf",
      sizeBytes: 245760,
      url: undefined,
    });
  });

  it("file: reads null when the embedded companion is absent", () => {
    const field: FormField = { field: "signed_pdf_id", type: "file" };
    expect(readFieldValue(field, { signed_pdf_id: "01j..." })).toBeNull();
  });

  it("file_multi: reads the embedded, _ids-stripped companion array as FileValue[]", () => {
    const field: FormField = { field: "attachment_ids", type: "file_multi" };
    const record: Row = {
      attachment_ids: ["1", "2"],
      attachments: [
        { id: "1", name: "Quote.pdf", content_type: "application/pdf", size_bytes: 128000 },
        { id: "2", name: "Spec.docx", content_type: "application/vnd.openxmlformats", size_bytes: 54000 },
      ],
    };
    expect(readFieldValue(field, record)).toEqual([
      { fileId: "1", name: "Quote.pdf", contentType: "application/pdf", sizeBytes: 128000, url: undefined },
      {
        fileId: "2",
        name: "Spec.docx",
        contentType: "application/vnd.openxmlformats",
        sizeBytes: 54000,
        url: undefined,
      },
    ]);
  });

  it("file_multi: reads an empty array when the embedded companion is absent", () => {
    const field: FormField = { field: "attachment_ids", type: "file_multi" };
    expect(readFieldValue(field, { attachment_ids: [] })).toEqual([]);
  });
});

describe("FieldInput", () => {
  it("text: renders the value and reports changes", () => {
    const onChange = renderField({ field: "name", type: "text" }, "Acme");
    fireEvent.change(screen.getByDisplayValue("Acme"), { target: { value: "Acme Inc" } });
    expect(onChange).toHaveBeenCalledWith("Acme Inc");
  });

  it("currency: prefixes the currency code read from currency_field and displays minor units as major-unit decimal", () => {
    // l10n-guide.md: amounts are stored as integer minor units — 10000 USD
    // cents displays as 100.
    renderField({ field: "amount", type: "currency", currency_field: "currency" }, 10000, { currency: "USD" });
    expect(screen.getByText("USD")).toBeTruthy();
    expect((screen.getByRole("spinbutton") as HTMLInputElement).value).toBe("100");
  });

  it("percent: displays as whole points and converts back to a 0-1 decimal on change", () => {
    const onChange = renderField({ field: "rate", type: "percent" }, 0.25);
    const input = screen.getByDisplayValue("25") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "50" } });
    expect(onChange).toHaveBeenCalledWith(0.5);
  });

  it("boolean: a checked checkbox that reports the toggled value", () => {
    const onChange = renderField({ field: "active", type: "boolean" }, true);
    const checkbox = screen.getByRole("checkbox") as HTMLInputElement;
    expect(checkbox.checked).toBe(true);
    fireEvent.click(checkbox);
    expect(onChange).toHaveBeenCalledWith(false);
  });

  it("toggle: renders as a switch role and reports the toggled value", () => {
    const onChange = renderField({ field: "enabled", type: "toggle" }, false);
    const toggle = screen.getByRole("switch") as HTMLInputElement;
    expect(toggle.checked).toBe(false);
    fireEvent.click(toggle);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("select: static options render and report the chosen value", async () => {
    const onChange = renderField(
      {
        field: "state",
        type: "select",
        options: [
          { value: "draft", label: "Draft" },
          { value: "done", label: "Done" },
        ],
      },
      "draft",
    );
    const trigger = screen.getByRole("combobox");
    expect(trigger.textContent).toContain("Draft");
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("option", { name: "Done" }));
    expect(onChange).toHaveBeenCalledWith("done");
  });

  it("radio: renders one input per option and reports the selected value", () => {
    const onChange = renderField(
      {
        field: "priority",
        type: "radio",
        options: [
          { value: "low", label: "Low" },
          { value: "high", label: "High" },
        ],
      },
      "low",
    );
    fireEvent.click(screen.getByLabelText("High"));
    expect(onChange).toHaveBeenCalledWith("high");
  });

  it("date_range: two inputs bound to range_start_field/range_end_field, not the field's own name", () => {
    const onChange = renderField(
      { field: "range", type: "date_range", range_start_field: "starts_on", range_end_field: "ends_on" },
      undefined,
      { starts_on: "2026-01-01", ends_on: "2026-01-31" },
    );
    fireEvent.change(screen.getByLabelText("range start"), { target: { value: "2026-02-01" } });
    expect(onChange).toHaveBeenCalledWith({ starts_on: "2026-02-01" });
  });

  it("duration: hours/minutes inputs compose back into total minutes", () => {
    const onChange = renderField({ field: "eta", type: "duration" }, 90);
    expect((screen.getByLabelText("eta hours") as HTMLInputElement).value).toBe("1");
    expect((screen.getByLabelText("eta minutes") as HTMLInputElement).value).toBe("30");
    fireEvent.change(screen.getByLabelText("eta minutes"), { target: { value: "45" } });
    expect(onChange).toHaveBeenCalledWith(105);
  });

  it("rating: fills stars up to the current value and reports the clicked star", () => {
    const onChange = renderField({ field: "score", type: "rating", max: 3 }, 1);
    fireEvent.click(screen.getByLabelText("3 stars"));
    expect(onChange).toHaveBeenCalledWith(3);
  });

  it("json: parses valid input and silently ignores invalid input mid-edit", () => {
    const onChange = renderField({ field: "meta", type: "json" }, { a: 1 });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "not json" } });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(textarea, { target: { value: '{"a":2}' } });
    expect(onChange).toHaveBeenCalledWith({ a: 2 });
  });

  it("separator/label: render with no bindable control and never call onChange", () => {
    renderField({ field: "_", type: "separator" }, undefined);
    expect(document.querySelector("hr")).toBeTruthy();
  });

  it("computed_display: renders the value read-only, ignoring `expression`", () => {
    renderField({ field: "total", type: "computed_display", expression: "line1 + line2" }, "42");
    expect(screen.getByText("42")).toBeTruthy();
  });

  it("custom: falls back to the raw value — no module component registry exists yet", () => {
    renderField({ field: "widget", type: "custom", component: "MyWidget" }, "raw-value");
    expect(screen.getByText("raw-value")).toBeTruthy();
  });

  it("relation with no resource, i.e. a plain field: falls back to the raw value instead of a picker", () => {
    renderField({ field: "owner_id", type: "relation" }, "01j-owner");
    expect(screen.getByText("01j-owner")).toBeTruthy();
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("relation with no resource_label_field: opens a picker using the registry's default labelField", async () => {
    renderField({ field: "owner_id", type: "relation", resource: "auth.user" }, null);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(await screen.findByRole("option", { name: "VIP" })).toBeTruthy();
  });

  it("relation targeting an unregistered/unloaded module: the picker shows a 'module not installed' state", async () => {
    resolveMetadataMock.mockResolvedValueOnce(undefined);
    renderField({ field: "owner_id", type: "relation", resource: "uninstalled.module" }, null);
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByText("Module not installed")).toBeTruthy();
  });

  it("tags: adding an option updates the displayed chips immediately and reports only the id array", async () => {
    const onChange = renderField(
      { field: "tag_ids", type: "tags", resource: "contacts.tag", resource_label_field: "name" },
      [{ id: "1", name: "VIP" }],
    );
    expect(await screen.findByText("VIP")).toBeTruthy();

    fireEvent.change(screen.getByPlaceholderText("Add tag_ids…"), { target: { value: "Lead" } });
    fireEvent.click(await screen.findByRole("option", { name: "Lead" })); // options query resolved
    expect(onChange).toHaveBeenCalledWith(["1", "2"]);
    expect(await screen.findByText("Lead")).toBeTruthy();

    fireEvent.click(screen.getByLabelText("Remove tag: VIP"));
    expect(onChange).toHaveBeenLastCalledWith(["2"]);
    // VIP can still legitimately appear in the "add" list now that it's no
    // longer selected — only its chip (and thus its remove button) must be
    // gone.
    expect(screen.queryByLabelText("Remove tag: VIP")).toBeNull();
  });

  it("tags: the add-input placeholder names the field's configured label, not just its field key", () => {
    renderField(
      { field: "tag_ids", type: "tags", label: "Skills", resource: "contacts.tag", resource_label_field: "name" },
      [],
    );
    expect(screen.getByPlaceholderText("Add Skills…")).toBeTruthy();
  });

  it("multi_select backed by a resource renders as a real (multi-select) RelationPicker, not a single picker", async () => {
    const onChange = renderField(
      { field: "tag_ids", type: "multi_select", resource: "contacts.tag", resource_label_field: "name" },
      [],
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.click(await screen.findByRole("option", { name: "VIP" }));
    expect(onChange).toHaveBeenCalledWith(["1"]);
    // Multi-select stays open for further selection, unlike single-select.
    expect(input.getAttribute("aria-expanded")).toBe("true");
  });

  it("select backed by a resource: resolves an existing raw id's display text via a filter[id][in] batch-fetch", async () => {
    renderField({ field: "state", type: "select", resource: "contacts.tag", resource_label_field: "name" }, "1");
    expect(await screen.findByDisplayValue("VIP")).toBeTruthy();
    expect(getMock).toHaveBeenCalledWith(
      "/tags",
      expect.objectContaining({ params: expect.objectContaining({ "filter[id][in]": "1" }) }),
    );
  });

  it("select backed by a resource with no resource_label_field: resolves display text via the registry's default labelField", async () => {
    renderField({ field: "state", type: "select", resource: "contacts.tag" }, "1");
    expect(await screen.findByDisplayValue("VIP")).toBeTruthy();
  });

  it("relation: shows the current RelationValue's display text and writes back only the raw id", async () => {
    const onChange = renderField(
      { field: "customer_id", type: "relation", resource: "contacts.contact", resource_label_field: "name" },
      { id: "1", display: "Acme Corp" },
    );
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Acme Corp");
    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Lead" }));
    expect(onChange).toHaveBeenCalledWith("2");
  });

  it("many2many: renders selected pills and writes back an id array", async () => {
    const onChange = renderField(
      { field: "tag_ids", type: "many2many", resource: "contacts.tag", resource_label_field: "name" },
      [{ id: "1", display: "VIP" }],
    );
    expect(screen.getByText("VIP")).toBeTruthy();
    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Lead" }));
    expect(onChange).toHaveBeenCalledWith(["1", "2"]);
  });

  it("file: shows the current FileValue's chip and writes back only the raw file id on removal", () => {
    const onChange = renderField(
      { field: "signed_pdf_id", type: "file" },
      {
        fileId: "01j...",
        name: "Contract.pdf",
        contentType: "application/pdf",
        sizeBytes: 245760,
      },
    );
    expect(screen.getByText("Contract.pdf")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove Contract.pdf" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("file_multi: renders every chip and writes back an id array on removal", () => {
    const onChange = renderField({ field: "attachment_ids", type: "file_multi" }, [
      { fileId: "1", name: "Quote.pdf", contentType: "application/pdf", sizeBytes: 128000 },
      { fileId: "2", name: "Spec.docx", contentType: "application/vnd.openxmlformats", sizeBytes: 54000 },
    ]);
    expect(screen.getByText("Quote.pdf")).toBeTruthy();
    expect(screen.getByText("Spec.docx")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove Quote.pdf" }));
    expect(onChange).toHaveBeenCalledWith(["2"]);
  });
});
