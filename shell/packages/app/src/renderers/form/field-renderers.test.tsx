import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetReportedConditionErrors } from "../../conditions/use-condition-evaluator.js";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

const { resolveResourceMock, resolveMetadataMock, getMock, tryResolveComponentMock, useActionMock, mutateAsyncMock } =
  vi.hoisted(() => ({
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
      meta: { cursor: null, has_more: false },
    })),
    tryResolveComponentMock: vi.fn() as ReturnType<typeof vi.fn> & ((name?: string) => unknown),
    mutateAsyncMock: vi.fn(async () => ({}) as { data?: Record<string, unknown> }),
    useActionMock: vi.fn(() => ({
      mutate: vi.fn(),
      mutateAsync: mutateAsyncMock,
      isPending: false,
      isError: false,
      error: null,
      data: undefined,
      reset: vi.fn(),
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
    componentRegistry: { tryResolve: tryResolveComponentMock },
  };
});
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useAction: useActionMock };
});
// barcode-detector/zxing-wasm's real decoder never runs in a unit test —
// only field-renderers.tsx's own on_scan_route orchestration is under test
// here, matching barcode-field.test.tsx's own scope split.
const { detectMock } = vi.hoisted(() => ({
  detectMock: vi.fn(async (_image: unknown) => [] as { rawValue: string }[]),
}));
vi.mock("barcode-detector/pure", () => ({
  BarcodeDetector: class {
    detect(image: unknown) {
      return detectMock(image);
    }
  },
}));

afterEach(() => {
  cleanup();
  resolveResourceMock.mockClear();
  resolveMetadataMock.mockClear();
  getMock.mockClear();
  tryResolveComponentMock.mockReset().mockReturnValue(undefined);
  useActionMock.mockClear();
  mutateAsyncMock.mockReset().mockResolvedValue({});
  detectMock.mockReset().mockResolvedValue([]);
});

// jsdom doesn't implement scrollIntoView (jsdom/jsdom#1695) — @goerp/sdk's
// Select (Radix-based for its single-select mode) calls it internally
// whenever the panel opens.
Element.prototype.scrollIntoView = vi.fn();

// BarcodeField's scan trigger needs a real getUserMedia/HTMLMediaElement.play
// to exist at all in jsdom, even for tests that never actually click it.
const getUserMediaMock = vi.fn(async () => ({ getTracks: () => [{ stop: vi.fn() }] }) as unknown as MediaStream);
Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia: getUserMediaMock }, configurable: true });
HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);

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

// Mirrors code-field.test.tsx's own paste() helper — CodeMirror's paste
// handling reads clipboardData.getData("text/plain") specifically, not any
// arbitrary MIME type, so this only serves text for that one type the same
// way a real paste event would.
function paste(target: Element, text: string): void {
  fireEvent.paste(target, { clipboardData: { getData: (type: string) => (type === "text/plain" ? text : "") } });
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

  it("barcode: writeFieldValue wraps a plain string under the field's own name, but passes an object through as an already-resolved patch", () => {
    const field: FormField = { field: "barcode", type: "barcode" };
    expect(writeFieldValue(field, "012345")).toEqual({ barcode: "012345" });
    expect(writeFieldValue(field, { barcode: "012345", product_name: "Widget" })).toEqual({
      barcode: "012345",
      product_name: "Widget",
    });
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

  it("boolean: labels the checkbox with the field's label and describes it with its help text", () => {
    renderField({ field: "active", type: "boolean", label: "Active", help_text: "Shown in pickers" }, false);
    const checkbox = screen.getByRole("checkbox", { name: "Active" });
    expect(checkbox.getAttribute("aria-describedby")).toBe(screen.getByText("Shown in pickers").id);
  });

  it("boolean: marks a required field's label with the same asterisk as FieldWrapper", () => {
    renderField({ field: "active", type: "boolean", label: "Active", required: true }, false);
    expect(screen.getByText("*").className).toContain("text-danger");
    expect(screen.getByRole("checkbox", { name: "Active" })).toBeTruthy();
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

  it("slider: renders at the current value and reports the new value on change", () => {
    const onChange = renderField({ field: "volume", type: "slider", min: 0, max: 100, step: 5 }, 30);
    const slider = screen.getByRole("slider") as HTMLInputElement;
    expect(slider.value).toBe("30");
    fireEvent.change(slider, { target: { value: "60" } });
    expect(onChange).toHaveBeenCalledWith(60);
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

  // paste() inserts at the cursor rather than replacing existing content,
  // so each case below starts from an empty field rather than editing one
  // value into another.
  it("json: silently ignores invalid input, leaving onChange uncalled", () => {
    const onChange = renderField({ field: "meta", type: "json" }, undefined);
    paste(screen.getByRole("textbox"), "not json");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("json: parses valid input and reports the parsed value", () => {
    const onChange = renderField({ field: "meta", type: "json" }, undefined);
    paste(screen.getByRole("textbox"), '{"a":2}');
    expect(onChange).toHaveBeenCalledWith({ a: 2 });
  });

  it("textarea: renders a plain textarea, unaffected by markdown's split into its own case", () => {
    const onChange = renderField({ field: "notes", type: "textarea" }, "plain text");
    const textarea = screen.getByDisplayValue("plain text");
    expect(textarea.tagName).toBe("TEXTAREA");
    fireEvent.change(textarea, { target: { value: "edited" } });
    expect(onChange).toHaveBeenCalledWith("edited");
  });

  it("markdown: renders MarkdownField's toolbar instead of a plain textarea", () => {
    renderField({ field: "notes", type: "markdown" }, "Some **text**");
    expect(screen.getByRole("toolbar", { name: "Formatting" })).toBeTruthy();
    expect(screen.queryByRole("textbox")?.tagName).not.toBe("TEXTAREA");
  });

  it("barcode: manual typing calls onChange directly and never triggers on_scan_route", () => {
    const onChange = renderField({ field: "barcode", type: "barcode" }, "");
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "012345" } });
    expect(onChange).toHaveBeenCalledWith("012345");
    expect(mutateAsyncMock).not.toHaveBeenCalled();
  });

  it("barcode: without on_scan_route declared, useAction is never even mounted", () => {
    renderField({ field: "barcode", type: "barcode" }, "");
    expect(useActionMock).not.toHaveBeenCalled();
  });

  it("barcode: without on_scan_route declared, a successful scan only calls onChange with the plain code", async () => {
    vi.useFakeTimers();
    try {
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      const onChange = renderField({ field: "barcode", type: "barcode" }, "");
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).toHaveBeenCalledWith("9781234567897");
      expect(onChange).toHaveBeenCalledTimes(1);
      expect(mutateAsyncMock).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("barcode: with on_scan_route declared, a successful scan writes the code immediately, then merges the lookup's resolved fields", async () => {
    vi.useFakeTimers();
    try {
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      mutateAsyncMock.mockResolvedValue({ data: { product_name: "Widget", unit_price: 9.99 } });
      const onChange = renderField({ field: "barcode", type: "barcode", on_scan_route: "inventory.findByBarcode" }, "");
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      // The scanned code writes immediately, before the lookup resolves.
      expect(onChange).toHaveBeenNthCalledWith(1, "9781234567897");
      expect(mutateAsyncMock).toHaveBeenCalledWith("9781234567897");
      // Flushes the still-pending mutateAsync().then() microtask — waitFor's
      // own real-timer polling would never fire while fake timers are active.
      await act(async () => {
        await Promise.resolve();
      });
      expect(onChange).toHaveBeenNthCalledWith(2, { product_name: "Widget", unit_price: 9.99 });
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    ["a network error", new TypeError("Failed to fetch")],
    ["a 5xx", new AppError({ code: "internal", message: "boom", httpStatus: 503 })],
  ])("barcode: %s from on_scan_route keeps the scanned code and shows an error toast", async (_, lookupError) => {
    vi.useFakeTimers();
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
    try {
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      mutateAsyncMock.mockRejectedValue(lookupError);
      const onChange = renderField({ field: "barcode", type: "barcode", on_scan_route: "inventory.findByBarcode" }, "");
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).toHaveBeenCalledWith("9781234567897");
      expect(onChange).toHaveBeenCalledTimes(1);
      expect(toastError).toHaveBeenCalledWith("Couldn't look up that code.");
    } finally {
      toastError.mockRestore();
      vi.useRealTimers();
    }
  });

  it("barcode: a 4xx from on_scan_route keeps the scanned code and leaves the toast to useAction", async () => {
    vi.useFakeTimers();
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
    try {
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      mutateAsyncMock.mockRejectedValue(new AppError({ code: "not_found", message: "not found", httpStatus: 404 }));
      const onChange = renderField({ field: "barcode", type: "barcode", on_scan_route: "inventory.findByBarcode" }, "");
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).toHaveBeenCalledWith("9781234567897");
      expect(onChange).toHaveBeenCalledTimes(1);
      expect(toastError).not.toHaveBeenCalled();
    } finally {
      toastError.mockRestore();
      vi.useRealTimers();
    }
  });

  it("separator/label: render with no bindable control and never call onChange", () => {
    renderField({ field: "_", type: "separator" }, undefined);
    expect(document.querySelector("hr")).toBeTruthy();
  });

  it("computed_display: falls back to the stored value when the field declares no expression", () => {
    renderField({ field: "total", type: "computed_display" }, "42");
    expect(screen.getByText("42")).toBeTruthy();
  });

  it("custom: falls back to the raw value when no component is declared", () => {
    renderField({ field: "widget", type: "custom" }, "raw-value");
    expect(screen.getByText("raw-value")).toBeTruthy();
  });

  it("custom: falls back to the raw value when the declared component isn't registered", () => {
    renderField({ field: "widget", type: "custom", component: "MyWidget" }, "raw-value");
    expect(screen.getByText("raw-value")).toBeTruthy();
  });

  it("custom: resolves a registered component, passing value/onChange/record/disabled/id/component_props", () => {
    function MyWidget(props: {
      value: unknown;
      onChange: (value: unknown) => void;
      record: Row;
      disabled: boolean;
      id?: string;
      label: string;
    }) {
      return (
        <button type="button" id={props.id} onClick={() => props.onChange("clicked")} disabled={props.disabled}>
          {String(props.value)}:{String(props.record.id)}:{props.label}
        </button>
      );
    }
    tryResolveComponentMock.mockReturnValue(MyWidget);
    const onChange = renderField(
      { field: "widget", type: "custom", component: "MyWidget", component_props: { label: "tagged" } },
      "raw-value",
      { id: "01j..." },
    );
    expect(tryResolveComponentMock).toHaveBeenCalledWith("MyWidget");
    const button = screen.getByRole("button", { name: "raw-value:01j...:tagged" });
    fireEvent.click(button);
    expect(onChange).toHaveBeenCalledWith("clicked");
  });

  it("custom: component_props can't shadow the real value/onChange/record/disabled wiring props", () => {
    function MyWidget(props: { value: unknown; onChange: (value: unknown) => void }) {
      return (
        <button type="button" onClick={() => props.onChange("clicked")}>
          {String(props.value)}
        </button>
      );
    }
    tryResolveComponentMock.mockReturnValue(MyWidget);
    const onChange = renderField(
      {
        field: "widget",
        type: "custom",
        component: "MyWidget",
        component_props: { value: "spoofed", onChange: () => {} },
      },
      "raw-value",
    );
    const button = screen.getByRole("button", { name: "raw-value" });
    fireEvent.click(button);
    expect(onChange).toHaveBeenCalledWith("clicked");
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

describe("FieldInput computed_display", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetReportedConditionErrors();
  });

  const field = (expression: string): FormField => ({ field: "margin", type: "computed_display", expression });

  function renderComputed(f: FormField, record: Row) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const ui = (r: Row) => (
      <QueryClientProvider client={client}>
        <FieldInput field={f} value={undefined} onChange={vi.fn()} record={r} />
      </QueryClientProvider>
    );
    const { rerender } = render(ui(record));
    return (next: Row) => rerender(ui(next));
  }

  it("evaluates the expression against the record", () => {
    renderComputed(field("record.amount - record.cost"), { amount: 10, cost: 4 });
    expect(screen.getByText("6")).toBeTruthy();
  });

  it("renders a PERCENT result as the formatted string", () => {
    renderComputed(field("PERCENT((record.amount - record.cost) / record.amount, 1)"), { amount: 10, cost: 4 });
    expect(screen.getByText("60.0%")).toBeTruthy();
  });

  it("does not truncate a ROUND result to the default three fraction digits", () => {
    renderComputed(field("ROUND(record.rate, 4)"), { rate: 1.23456 });
    expect(
      screen.getByText(new Intl.NumberFormat(undefined, { maximumFractionDigits: 15 }).format(1.2346)),
    ).toBeTruthy();
  });

  it("reads a decimal string field as a number", () => {
    renderComputed(field("record.price * record.qty"), { price: "12.50", qty: 4 });
    expect(screen.getByText("50")).toBeTruthy();
  });

  it("re-evaluates when a referenced field changes", () => {
    const update = renderComputed(field("record.a + record.b"), { a: 1, b: 2 });
    expect(screen.getByText("3")).toBeTruthy();
    update({ a: 1, b: 5 });
    expect(screen.getByText("6")).toBeTruthy();
    expect(screen.queryByText("3")).toBeNull();
  });

  it("shows a failure, not a blank or a zero, when an operand is empty", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    renderComputed(field("record.amount - record.cost"), { amount: 10, cost: null });
    const failure = screen.getByText("Can't compute", { exact: false });
    expect(failure.getAttribute("title")).toContain("record.cost is empty");
    expect(failure.textContent).toContain("record.cost is empty");
    expect(consoleError).not.toHaveBeenCalled();
  });

  it("shows a failure on division by zero", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderComputed(field("record.a / record.b"), { a: 1, b: 0 });
    expect(screen.getByText("Can't compute", { exact: false }).getAttribute("title")).toContain("division by zero");
  });

  it("recovers once the operand is fixed", () => {
    const update = renderComputed(field("record.a / record.b"), { a: 1, b: 0 });
    expect(screen.getByText("Can't compute", { exact: false })).toBeTruthy();
    update({ a: 3, b: 2 });
    expect(screen.queryByText("Can't compute", { exact: false })).toBeNull();
    expect(screen.getByText("1.5")).toBeTruthy();
  });

  it("renders a zero result as 0, never -0", () => {
    renderComputed(field("-record.a"), { a: 0 });
    expect(screen.getByText("0")).toBeTruthy();
    expect(screen.queryByText("-0")).toBeNull();
  });

  it("shows a failure, without throwing, for a malformed expression", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => renderComputed(field("record.a +"), { a: 1 })).not.toThrow();
    expect(screen.getByText("Can't compute", { exact: false })).toBeTruthy();
    expect(String(consoleError.mock.calls[0]?.[0])).toContain('field "margin" expression');
  });

  it("rejects a boolean-only construct rather than evaluating it", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderComputed(field("record.a = 1"), { a: 1 });
    expect(screen.getByText("Can't compute", { exact: false })).toBeTruthy();
  });
});
