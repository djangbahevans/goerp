import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TimezoneSelect } from "./timezone-select.js";

// jsdom doesn't implement scrollIntoView at all — code-select.tsx calls it
// to keep the keyboard-highlighted option visible within the panel's
// scrollable area.
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(cleanup);

describe("TimezoneSelect", () => {
  it("shows the placeholder when nothing is selected", () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} placeholder="Pick a timezone" />);
    expect((screen.getByRole("combobox") as HTMLInputElement).placeholder).toBe("Pick a timezone");
  });

  it("shows the selected timezone with underscores rendered as spaces in the trigger when closed", () => {
    render(<TimezoneSelect value="America/New_York" onChange={vi.fn()} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("America/New York");
  });

  it("opens on focus and lists every timezone Intl.supportedValuesOf reports, alphabetized", async () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("combobox"));
    const options = await screen.findAllByRole("option");
    expect(options.length).toBe(Intl.supportedValuesOf("timeZone").length);
    const names = options.map((o) => o.textContent);
    expect([...names].sort((a, b) => (a ?? "").localeCompare(b ?? ""))).toEqual(names);
  });

  it("filters by a space-typed query against an underscore-containing identifier", async () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "new york" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["America/New York"]);
  });

  it("filters by the raw identifier too", async () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "america/new_york" } });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["America/New York"]);
  });

  it("commits the picked identifier unmodified (not the space-formatted display string) and closes", async () => {
    const onChange = vi.fn();
    render(<TimezoneSelect value="" onChange={onChange} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "new york" } });
    fireEvent.click(await screen.findByRole("option", { name: "America/New York" }));
    expect(onChange).toHaveBeenCalledWith("America/New_York");
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", async () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "zzzznotatimezone" } });
    const empty = await screen.findByText('No results for "zzzznotatimezone"');
    expect(empty.getAttribute("aria-live")).toBe("polite");
  });

  it("a labeled clear button unsets the value", () => {
    const onChange = vi.fn();
    render(<TimezoneSelect value="America/New_York" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear America/New York" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("no clear button when there's no selected value", () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("a value outside the current Intl.supportedValuesOf result still displays and stays clearable", () => {
    const onChange = vi.fn();
    render(<TimezoneSelect value="Etc/Not_A_Real_Zone" onChange={onChange} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Etc/Not A Real Zone");
    fireEvent.click(screen.getByRole("button", { name: "Clear Etc/Not A Real Zone" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("closes on a click outside the input and the dropdown", () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    fireEvent.mouseDown(document.body);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("portals the dropdown to document.body, escaping an overflow: hidden ancestor", () => {
    const { container } = render(
      <div style={{ overflow: "hidden", height: "10px" }}>
        <TimezoneSelect value="" onChange={vi.fn()} />
      </div>,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    const listbox = screen.getByRole("listbox");
    expect(container.contains(listbox)).toBe(false);
    expect(document.body.contains(listbox)).toBe(true);
  });

  it("disables the input when disabled", () => {
    render(<TimezoneSelect value="" onChange={vi.fn()} disabled />);
    expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  });

  it("falls back to a plain text input when Intl.supportedValuesOf is unavailable", () => {
    const spy = vi.spyOn(Intl, "supportedValuesOf").mockImplementation(() => {
      throw new Error("unsupported");
    });
    const onChange = vi.fn();
    render(<TimezoneSelect value="Africa/Accra" onChange={onChange} placeholder="Pick a timezone" />);
    expect(screen.queryByRole("combobox")).toBeNull();
    const input = screen.getByPlaceholderText("Pick a timezone") as HTMLInputElement;
    expect(input.value).toBe("Africa/Accra");
    fireEvent.change(input, { target: { value: "typed" } });
    expect(onChange).toHaveBeenCalledWith("typed");
    spy.mockRestore();
  });
});
