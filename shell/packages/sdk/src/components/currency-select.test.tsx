import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CurrencySelect } from "./currency-select.js";

// jsdom doesn't implement scrollIntoView at all — code-select.tsx calls it
// to keep the keyboard-highlighted option visible within the panel's
// scrollable area.
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(cleanup);

describe("CurrencySelect", () => {
  it("shows the placeholder when nothing is selected", () => {
    render(<CurrencySelect value="" onChange={vi.fn()} placeholder="Pick a currency" />);
    expect((screen.getByRole("combobox") as HTMLInputElement).placeholder).toBe("Pick a currency");
  });

  it("shows the selected raw code in the trigger when closed", () => {
    render(<CurrencySelect value="USD" onChange={vi.fn()} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("USD");
  });

  it("opens on focus and lists every currency Intl.supportedValuesOf reports, alphabetized by code", async () => {
    render(<CurrencySelect value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("combobox"));
    const options = await screen.findAllByRole("option");
    expect(options.length).toBe(Intl.supportedValuesOf("currency").length);
    const codes = options.map((o) => o.textContent);
    expect([...codes].sort((a, b) => (a ?? "").localeCompare(b ?? ""))).toEqual(codes);
  });

  it("filters by the raw code", async () => {
    render(<CurrencySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "eur" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["EUR"]);
  });

  it("commits the picked code and closes", async () => {
    const onChange = vi.fn();
    render(<CurrencySelect value="" onChange={onChange} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "eur" } });
    fireEvent.click(await screen.findByRole("option", { name: "EUR" }));
    expect(onChange).toHaveBeenCalledWith("EUR");
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", async () => {
    render(<CurrencySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "zzznotacurrency" } });
    const empty = await screen.findByText('No results for "zzznotacurrency"');
    expect(empty.getAttribute("aria-live")).toBe("polite");
  });

  it("a labeled clear button unsets the value", () => {
    const onChange = vi.fn();
    render(<CurrencySelect value="USD" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear USD" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("no clear button when there's no selected value", () => {
    render(<CurrencySelect value="" onChange={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("a value outside the current Intl.supportedValuesOf result still displays and stays clearable", () => {
    const onChange = vi.fn();
    render(<CurrencySelect value="ZZZ" onChange={onChange} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("ZZZ");
    fireEvent.click(screen.getByRole("button", { name: "Clear ZZZ" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("closes on a click outside the input and the dropdown", () => {
    render(<CurrencySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    fireEvent.mouseDown(document.body);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("portals the dropdown to document.body, escaping an overflow: hidden ancestor", () => {
    const { container } = render(
      <div style={{ overflow: "hidden", height: "10px" }}>
        <CurrencySelect value="" onChange={vi.fn()} />
      </div>,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    const listbox = screen.getByRole("listbox");
    expect(container.contains(listbox)).toBe(false);
    expect(document.body.contains(listbox)).toBe(true);
  });

  it("disables the input when disabled", () => {
    render(<CurrencySelect value="" onChange={vi.fn()} disabled />);
    expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  });
});
