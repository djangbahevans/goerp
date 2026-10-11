import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FieldWrapper } from "./field-wrapper.js";
import { PhoneInput, type PhoneInputProps } from "./phone-input.js";

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(cleanup);

function Controlled({ value: initial, onChange, ...props }: PhoneInputProps) {
  const [value, setValue] = useState(initial);
  return (
    <PhoneInput
      {...props}
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange(next);
      }}
    />
  );
}

function chooseCountry(query: string, label: string): void {
  fireEvent.click(screen.getByRole("button", { name: /Country calling code/ }));
  const input = screen.getByRole("combobox", { name: "Search countries" });
  fireEvent.change(input, { target: { value: query } });
  fireEvent.click(screen.getByRole("option", { name: label }));
}

describe("PhoneInput", () => {
  it.each(["", "050 123 4567", "+233 50-123-4567", "+1 202 555 0123", "+999 123", "0044 123"])(
    "preserves %s on mount without reporting a change",
    (value) => {
      const onChange = vi.fn();
      render(<PhoneInput value={value} onChange={onChange} aria-label="Phone" />);
      expect(onChange).not.toHaveBeenCalled();
      const input = screen.getByRole("textbox", { name: "Phone" }) as HTMLInputElement;
      expect(input.type).toBe("tel");
      expect(input.value).toBe(
        value.startsWith("+233 ") || value.startsWith("+1 ") ? value.split(" ").slice(1).join(" ") : value,
      );
    },
  );

  it("infers a unique calling code but does not guess a country for a shared code", () => {
    const { rerender } = render(<PhoneInput value="+233501234567" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Country calling code Ghana (+233)" })).toBeTruthy();
    rerender(<PhoneInput value="+12025550123" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Country calling code Calling code +1" })).toBeTruthy();
    rerender(<PhoneInput value="0501234567" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Country calling code Choose country" })).toBeTruthy();
  });

  it("retains an explicit country for an empty value without making the model dirty", () => {
    const onChange = vi.fn();
    render(<Controlled value="" onChange={onChange} aria-label="Phone" />);
    chooseCountry("Ghana", "Ghana (+233)");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Country calling code Ghana (+233)" })).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "Phone" }), { target: { value: "0501234567" } });
    expect(onChange).toHaveBeenLastCalledWith("+233 0501234567");
  });

  it("preserves the national number and leading zero when choosing a country", () => {
    const onChange = vi.fn();
    render(<Controlled value="050 123-4567 ext 9" onChange={onChange} />);
    chooseCountry("GH", "Ghana (+233)");
    expect(onChange).toHaveBeenLastCalledWith("+233 050 123-4567 ext 9");
    chooseCountry("+234", "Nigeria (+234)");
    expect(onChange).toHaveBeenLastCalledWith("+234 050 123-4567 ext 9");
  });

  it("replaces the existing calling code while preserving the subscriber text", () => {
    const onChange = vi.fn();
    render(<Controlled value="+1 (202) 555-0123 ext 4" onChange={onChange} />);
    chooseCountry("Ghana", "Ghana (+233)");
    expect(onChange).toHaveBeenLastCalledWith("+233 (202) 555-0123 ext 4");
  });

  it("preserves a leading plus in malformed subscriber text when changing country", () => {
    const onChange = vi.fn();
    render(<Controlled value="+233 +501234567" onChange={onChange} />);
    chooseCountry("Nigeria", "Nigeria (+234)");
    expect(onChange).toHaveBeenLastCalledWith("+234 +501234567");
    expect(screen.getByRole("button", { name: "Country calling code Nigeria (+234)" })).toBeTruthy();
  });

  it("preserves explicit choices within shared codes and re-infers external values", () => {
    const onChange = vi.fn();
    const { rerender } = render(<PhoneInput value="+1 2025550123" onChange={onChange} />);
    chooseCountry("Canada", "Canada (+1)");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Country calling code Canada (+1)" })).toBeTruthy();
    rerender(<PhoneInput value="+234 8012345678" onChange={onChange} />);
    expect(screen.getByRole("button", { name: "Country calling code Nigeria (+234)" })).toBeTruthy();
    rerender(<PhoneInput value="+1 2025550123" onChange={onChange} />);
    expect(screen.getByRole("button", { name: "Country calling code Calling code +1" })).toBeTruthy();
  });

  it("edits one string, clears to empty, and accepts a pasted international value verbatim", () => {
    const onChange = vi.fn();
    render(<Controlled value="+233 501234567" onChange={onChange} aria-label="Phone" />);
    const input = screen.getByRole("textbox", { name: "Phone" });
    fireEvent.change(input, { target: { value: "241234567" } });
    expect(onChange).toHaveBeenLastCalledWith("+233 241234567");
    fireEvent.change(input, { target: { value: "" } });
    expect(onChange).toHaveBeenLastCalledWith("");
    fireEvent.change(input, { target: { value: "+234 801-234-5678" } });
    expect(onChange).toHaveBeenLastCalledWith("+234 801-234-5678");
    expect(screen.getByRole("button", { name: "Country calling code Nigeria (+234)" })).toBeTruthy();
  });

  it("keeps national edits verbatim until a country is chosen", () => {
    const onChange = vi.fn();
    render(<Controlled value="" onChange={onChange} aria-label="Phone" />);
    fireEvent.change(screen.getByRole("textbox", { name: "Phone" }), { target: { value: "050 1234567" } });
    expect(onChange).toHaveBeenLastCalledWith("050 1234567");
  });

  it("preserves all digits when choosing a country for an unrecognized prefix", () => {
    const onChange = vi.fn();
    render(<Controlled value="+999 123" onChange={onChange} />);
    chooseCountry("Ghana", "Ghana (+233)");
    expect(onChange).toHaveBeenLastCalledWith("+233 999 123");
  });

  it("wires the label to the telephone input and errors to both controls without duplicate ids", () => {
    const { container } = render(
      <FieldWrapper label="Phone" error="Invalid phone" description="Contact number" required>
        <PhoneInput value="" onChange={vi.fn()} />
      </FieldWrapper>,
    );
    const phone = screen.getByRole("textbox", { name: "Phone" }) as HTMLInputElement;
    const country = screen.getByRole("button", { name: "Country calling code Choose country" }) as HTMLButtonElement;
    expect(phone.required).toBe(true);
    expect(country.hasAttribute("required")).toBe(false);
    expect(phone.id).not.toBe(country.id);
    for (const input of [phone, country]) {
      expect(input.getAttribute("aria-invalid")).toBe("true");
      const ids = input.getAttribute("aria-describedby")?.split(" ") ?? [];
      expect(ids.map((id) => container.querySelector(`[id="${id}"]`)?.textContent)).toEqual([
        "Contact number",
        "Invalid phone",
      ]);
    }
  });

  it.each(["disabled", "readOnly"] as const)("prevents country editing when %s", (state) => {
    render(<PhoneInput value="+233 501234567" onChange={vi.fn()} {...{ [state]: true }} aria-label="Phone" />);
    expect((screen.getByRole("button", { name: /Country calling code/ }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("textbox", { name: "Phone" }) as HTMLInputElement)[state]).toBe(true);
  });

  it("isolates popup focus and validation from the telephone field", () => {
    render(
      <FieldWrapper label="Phone" error="Invalid phone" required>
        <PhoneInput value="+233 501234567" onChange={vi.fn()} />
      </FieldWrapper>,
    );
    const trigger = screen.getByRole("button", { name: /Country calling code/ });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(trigger);
    const search = screen.getByRole("combobox", { name: "Search countries" }) as HTMLInputElement;
    expect(search.required).toBe(false);
    expect(search.hasAttribute("aria-invalid")).toBe(false);
    search.focus();
    fireEvent.mouseDown(screen.getByRole("listbox"));
    expect(document.activeElement).toBe(search);
    fireEvent.change(search, { target: { value: "No such country" } });
    expect(screen.getByText("No countries found")).toBeTruthy();
    const event = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    fireEvent(search, event);
    expect(event.defaultPrevented).toBe(true);
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("supports keyboard filtering, selection, Escape and Tab dismissal", () => {
    const onChange = vi.fn();
    render(<Controlled value="0501234567" onChange={onChange} />);
    const trigger = screen.getByRole("button", { name: /Country calling code/ });
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    let input = screen.getByRole("combobox", { name: "Search countries" });
    fireEvent.change(input, { target: { value: "Ghana" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith("+233 0501234567");
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    input = screen.getByRole("combobox", { name: "Search countries" });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    input = screen.getByRole("combobox", { name: "Search countries" });
    fireEvent.keyDown(input, { key: "Tab" });
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});
