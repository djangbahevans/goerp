import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RichTextField } from "./rich-text-field.js";

afterEach(cleanup);

describe("RichTextField", () => {
  it("renders the label and value", () => {
    render(<RichTextField label="Description" value="Hello" onChange={() => {}} />);
    expect(screen.getByLabelText("Description")).toBeTruthy();
    expect(screen.getByRole("textbox").textContent).toBe("Hello");
  });

  it("calls onChange with the new value", () => {
    const onChange = vi.fn();
    render(<RichTextField label="Description" value="" onChange={onChange} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "New text" } });
    expect(onChange).toHaveBeenCalledWith("New text");
  });

  it("renders an error message", () => {
    render(<RichTextField label="Description" value="" onChange={() => {}} error="Too long" />);
    expect(screen.getByRole("alert").textContent).toBe("Too long");
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  });

  it("defaults to 3 rows", () => {
    render(<RichTextField label="Description" value="" onChange={() => {}} />);
    expect(screen.getByRole("textbox").getAttribute("rows")).toBe("3");
  });
});
