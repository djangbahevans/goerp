import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ColorPicker } from "./color-picker.js";

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

  it("renders an error message", () => {
    render(<ColorPicker label="Tag color" onChange={() => {}} error="Invalid color" />);
    expect(screen.getByRole("alert").textContent).toBe("Invalid color");
  });
});
