import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LanguageSelect } from "./language-select.js";

afterEach(cleanup);

describe("LanguageSelect", () => {
  it("shows the placeholder when nothing is selected", () => {
    render(<LanguageSelect value="" onChange={vi.fn()} placeholder="Pick a language" />);
    expect((screen.getByRole("combobox") as HTMLInputElement).placeholder).toBe("Pick a language");
  });

  it("shows the selected language's resolved name in the trigger when closed", () => {
    render(<LanguageSelect value="fr" onChange={vi.fn()} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("French");
  });

  it("opens on focus and lists all 183 options", async () => {
    render(<LanguageSelect value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("combobox"));
    const options = await screen.findAllByRole("option");
    expect(options.length).toBe(183);
  });

  it("filters by resolved name", async () => {
    render(<LanguageSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "French" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["French"]);
  });

  it("commits the picked tag and closes", async () => {
    const onChange = vi.fn();
    render(<LanguageSelect value="" onChange={onChange} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "French" } });
    fireEvent.click(await screen.findByRole("option", { name: "French" }));
    expect(onChange).toHaveBeenCalledWith("fr");
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", async () => {
    render(<LanguageSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "notalanguage" } });
    const empty = await screen.findByText('No results for "notalanguage"');
    expect(empty.getAttribute("aria-live")).toBe("polite");
  });

  it("a labeled clear button unsets the value", () => {
    const onChange = vi.fn();
    render(<LanguageSelect value="fr" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear French" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("no clear button when there's no selected value", () => {
    render(<LanguageSelect value="" onChange={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("a value outside the bundled dataset still displays and stays clearable", () => {
    const onChange = vi.fn();
    render(<LanguageSelect value="zz" onChange={onChange} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("zz");
    fireEvent.click(screen.getByRole("button", { name: "Clear zz" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("closes on a click outside the input and the dropdown", () => {
    render(<LanguageSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    fireEvent.mouseDown(document.body);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("portals the dropdown to document.body, escaping an overflow: hidden ancestor", () => {
    const { container } = render(
      <div style={{ overflow: "hidden", height: "10px" }}>
        <LanguageSelect value="" onChange={vi.fn()} />
      </div>,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    const listbox = screen.getByRole("listbox");
    expect(container.contains(listbox)).toBe(false);
    expect(document.body.contains(listbox)).toBe(true);
  });

  it("disables the input when disabled", () => {
    render(<LanguageSelect value="" onChange={vi.fn()} disabled />);
    expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  });
});
