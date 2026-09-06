import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodeField } from "./code-field.js";

afterEach(cleanup);

describe("CodeField", () => {
  it("renders the label and value", () => {
    render(<CodeField label="Script" value="print(1)" onChange={() => {}} />);
    expect(screen.getByLabelText("Script")).toBeTruthy();
    expect(screen.getByRole("textbox").textContent).toBe("print(1)");
  });

  it("surfaces the language as a data attribute", () => {
    render(<CodeField label="Script" value="" onChange={() => {}} language="python" />);
    expect(screen.getByRole("textbox").getAttribute("data-language")).toBe("python");
  });

  it("calls onChange with the new value", () => {
    const onChange = vi.fn();
    render(<CodeField label="Script" value="" onChange={onChange} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "print(2)" } });
    expect(onChange).toHaveBeenCalledWith("print(2)");
  });

  it("renders an error message", () => {
    render(<CodeField label="Script" value="" onChange={() => {}} error="Invalid syntax" />);
    expect(screen.getByRole("alert").textContent).toBe("Invalid syntax");
  });
});
