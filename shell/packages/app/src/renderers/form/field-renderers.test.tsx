import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

const { resolveResourceMock, getMock } = vi.hoisted(() => ({
  resolveResourceMock: vi.fn(async () => ({ listPath: "/tags" })),
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
  return { ...actual, resourceRegistry: { resolve: resolveResourceMock } };
});

afterEach(() => {
  cleanup();
  resolveResourceMock.mockClear();
  getMock.mockClear();
});

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
});

describe("FieldInput", () => {
  it("text: renders the value and reports changes", () => {
    const onChange = renderField({ field: "name", type: "text" }, "Acme");
    fireEvent.change(screen.getByDisplayValue("Acme"), { target: { value: "Acme Inc" } });
    expect(onChange).toHaveBeenCalledWith("Acme Inc");
  });

  it("currency: prefixes the currency code read from currency_field", () => {
    renderField({ field: "amount", type: "currency", currency_field: "currency" }, 100, { currency: "USD" });
    expect(screen.getByText("USD")).toBeTruthy();
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

  it("toggle: renders as a switch role", () => {
    renderField({ field: "enabled", type: "toggle" }, false);
    expect(screen.getByRole("switch")).toBeTruthy();
  });

  it("select: static options render, with a stable empty option, and report the chosen value", () => {
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
    fireEvent.change(screen.getByDisplayValue("Draft"), { target: { value: "done" } });
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

  it("relation/tags/user_select with no resource_label_field: falls back to the raw value instead of a picker", () => {
    renderField({ field: "owner_id", type: "relation", resource: "auth.user" }, "01j-owner");
    expect(screen.getByText("01j-owner")).toBeTruthy();
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("tags: adding an option updates the displayed chips immediately and reports only the id array", async () => {
    const onChange = renderField(
      { field: "tag_ids", type: "tags", resource: "contacts.tag", resource_label_field: "name" },
      [{ id: "1", name: "VIP" }],
    );
    expect(await screen.findByText("VIP")).toBeTruthy();
    await screen.findByRole("option", { name: "Lead" }); // options query resolved

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "2" } });
    expect(onChange).toHaveBeenCalledWith(["1", "2"]);
    expect(await screen.findByText("Lead")).toBeTruthy();

    fireEvent.click(screen.getByLabelText("Remove VIP"));
    expect(onChange).toHaveBeenLastCalledWith(["2"]);
    // VIP can still legitimately appear in the "add" dropdown's own option
    // list now that it's no longer selected — only its chip (and thus its
    // remove button) must be gone.
    expect(screen.queryByLabelText("Remove VIP")).toBeNull();
  });

  it("multi_select backed by a resource renders as a multi-select, not a single picker", async () => {
    renderField({ field: "tag_ids", type: "multi_select", resource: "contacts.tag", resource_label_field: "name" }, []);
    const select = await screen.findByRole("listbox");
    expect((select as HTMLSelectElement).multiple).toBe(true);
  });
});
