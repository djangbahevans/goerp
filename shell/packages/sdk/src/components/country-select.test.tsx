import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CountrySelect } from "./country-select.js";

afterEach(cleanup);

describe("CountrySelect", () => {
  it("shows the placeholder when nothing is selected", () => {
    render(<CountrySelect value="" onChange={vi.fn()} placeholder="Pick a country" />);
    expect((screen.getByRole("combobox") as HTMLInputElement).placeholder).toBe("Pick a country");
  });

  it("shows the selected country's resolved name in the trigger when closed", () => {
    render(<CountrySelect value="GH" onChange={vi.fn()} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Ghana");
  });

  it("opens on focus and lists matching options, alphabetized by name", async () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("combobox"));
    const options = await screen.findAllByRole("option");
    expect(options.length).toBe(249);
    const names = options.map((o) => o.textContent);
    expect([...names].sort((a, b) => (a ?? "").localeCompare(b ?? ""))).toEqual(names);
  });

  it("filters by resolved name", async () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "Ghana" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["Ghana"]);
  });

  it("filters by raw code too, even when the code isn't a substring of the name", async () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "jp" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["Japan"]);
  });

  it("commits the picked code and closes", async () => {
    const onChange = vi.fn();
    render(<CountrySelect value="" onChange={onChange} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "Ghana" } });
    fireEvent.click(await screen.findByRole("option", { name: "Ghana" }));
    expect(onChange).toHaveBeenCalledWith("GH");
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", async () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "zzzznotacountry" } });
    const empty = await screen.findByText('No results for "zzzznotacountry"');
    expect(empty.getAttribute("aria-live")).toBe("polite");
  });

  it("a labeled clear button unsets the value", () => {
    const onChange = vi.fn();
    render(<CountrySelect value="GH" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear Ghana" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("no clear button when there's no selected value", () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("a value outside the bundled dataset still displays and stays clearable", () => {
    const onChange = vi.fn();
    render(<CountrySelect value="QQ" onChange={onChange} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("QQ");
    fireEvent.click(screen.getByRole("button", { name: "Clear QQ" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("closes when focus leaves the widget without a selection", () => {
    render(<CountrySelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    fireEvent.blur(input);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("disables the input when disabled", () => {
    render(<CountrySelect value="" onChange={vi.fn()} disabled />);
    expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  });
});
