import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type EntryWire, toEntry } from "./config-api.js";
import { ConfigField } from "./config-field.js";
import { draftOf } from "./config-widgets.js";

function mount(overrides: Partial<EntryWire> = {}, error?: string) {
  const entry = toEntry({
    key: "setting",
    label: "Setting",
    type: "string",
    required: false,
    encrypted: false,
    generated: false,
    restart_required: false,
    default: null,
    is_set: false,
    value: "GH",
    ...overrides,
  });
  const onChange = vi.fn();
  render(<ConfigField entry={entry} draft={draftOf(entry)} onChange={onChange} error={error} />);
  return onChange;
}

afterEach(cleanup);

describe("settings controls", () => {
  it.each(["country_select", "language_select", "timezone_select", "currency_select", "icon_picker"])(
    "renders the labelled %s picker",
    (field_type) => {
      mount({ field_type });
      expect(screen.getByRole("combobox", { name: "Setting" })).toBeDefined();
    },
  );

  it.each(["text", "email", "url", "phone", "textarea", "password"])("edits through %s", (field_type) => {
    const onChange = mount({ field_type, value: "" });
    fireEvent.change(screen.getByLabelText("Setting"), { target: { value: "value" } });
    expect(onChange).toHaveBeenCalledWith("value");
  });

  it("edits a labelled color picker", () => {
    const onChange = mount({ field_type: "color_picker", value: "#000000" });
    fireEvent.change(screen.getByLabelText("Setting"), { target: { value: "#112233" } });
    expect(onChange).toHaveBeenCalledWith("#112233");
  });

  it("renders scalar radio choices without changing their wire representation", () => {
    const onChange = mount({
      type: "integer",
      field_type: "radio",
      options: [
        { value: "1", label: "One" },
        { value: "2", label: "Two" },
      ],
      value: 1,
    });
    fireEvent.click(screen.getByRole("radio", { name: "Two" }));
    expect(onChange).toHaveBeenCalledWith("2");
  });

  it("renders an explicit boolean as a checkbox", () => {
    const onChange = mount({ type: "boolean", field_type: "boolean", value: false });
    fireEvent.click(screen.getByRole("checkbox", { name: "Setting" }));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("associates radio validation with the group and its labelled first option", () => {
    mount({ field_type: "radio", options: [{ value: "GH", label: "Ghana" }] }, "Choose an allowed country");
    const group = screen.getByRole("radiogroup", { name: "Setting" });
    expect(group.getAttribute("aria-invalid")).toBe("true");
    expect(document.getElementById(group.getAttribute("aria-describedby") ?? "")?.textContent).toBe(
      "Choose an allowed country",
    );
    expect(screen.getByLabelText("Setting", { selector: "input" })).toBe(screen.getByRole("radio", { name: /Ghana/ }));
  });

  it("shows a declaration diagnostic without an editable fallback", () => {
    mount({ field_type: "relation" });
    expect(screen.getByRole("alert").textContent).toContain("Unsupported settings field type");
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("keeps an encrypted country with options in a password input", () => {
    mount({
      field_type: "country_select",
      options: [{ value: "GH", label: "Ghana" }],
      encrypted: true,
      is_set: true,
      value: "***",
    });
    expect((screen.getByLabelText("Setting") as HTMLInputElement).type).toBe("password");
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("keeps generated values read-only even with a picker declaration", () => {
    mount({ field_type: "country_select", generated: true });
    expect(screen.getByText("GH")).toBeDefined();
    expect(screen.queryByRole("combobox")).toBeNull();
  });
});
