import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ColorPicker } from "./color-picker.js";
import { FieldWrapper } from "./field-wrapper.js";

afterEach(cleanup);

describe("ColorPicker", () => {
  it("renders the label and value", () => {
    render(<ColorPicker label="Tag color" value="#ff0000" onChange={() => {}} />);
    const input = screen.getByLabelText("Tag color") as HTMLInputElement;
    expect(input.type).toBe("color");
    expect(input.value).toBe("#ff0000");
  });

  it("defaults to black when no value is set", () => {
    render(<ColorPicker label="Tag color" onChange={() => {}} />);
    const input = screen.getByLabelText("Tag color") as HTMLInputElement;
    expect(input.value).toBe("#000000");
  });

  it("calls onChange with the new value", () => {
    const onChange = vi.fn();
    render(<ColorPicker label="Tag color" value="#000000" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Tag color"), { target: { value: "#00ff00" } });
    expect(onChange).toHaveBeenCalledWith("#00ff00");
  });

  it("inherits the wrapper label, validation and description", () => {
    render(
      <FieldWrapper label="Brand color" description="Invoice heading" error="Choose a color" required>
        <ColorPicker value="#112233" onChange={() => {}} />
      </FieldWrapper>,
    );
    const input = screen.getByLabelText(/Brand color/) as HTMLInputElement;
    expect(input.required).toBe(true);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const description = input
      .getAttribute("aria-describedby")
      ?.split(" ")
      .map((id) => document.getElementById(id)?.textContent);
    expect(description).toEqual(["Invoice heading", "Choose a color"]);
  });

  it("renders an error message", () => {
    render(<ColorPicker label="Tag color" onChange={() => {}} error="Invalid color" />);
    expect(screen.getByRole("alert").textContent).toBe("Invalid color");
  });
});
