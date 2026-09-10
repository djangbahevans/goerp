import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToggleField } from "./toggle-field.js";

afterEach(cleanup);

describe("ToggleField", () => {
  it("renders as a checked switch when value is true", () => {
    render(<ToggleField value={true} onChange={vi.fn()} />);
    const toggle = screen.getByRole("switch") as HTMLInputElement;
    expect(toggle.checked).toBe(true);
  });

  it("renders as an unchecked switch when value is false", () => {
    render(<ToggleField value={false} onChange={vi.fn()} />);
    const toggle = screen.getByRole("switch") as HTMLInputElement;
    expect(toggle.checked).toBe(false);
  });

  it("reports the toggled value on click", () => {
    const onChange = vi.fn();
    render(<ToggleField value={false} onChange={onChange} />);
    fireEvent.click(screen.getByRole("switch"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("is disabled and non-interactive when disabled", () => {
    const onChange = vi.fn();
    render(<ToggleField value={false} onChange={onChange} disabled />);
    const toggle = screen.getByRole("switch") as HTMLInputElement;
    expect(toggle.disabled).toBe(true);
  });

  it("reflects the checked state on the visible track via a data attribute", () => {
    const { rerender } = render(<ToggleField value={false} onChange={vi.fn()} />);
    const track = screen.getByRole("switch").closest("label");
    expect(track?.getAttribute("data-checked")).toBe("false");
    rerender(<ToggleField value={true} onChange={vi.fn()} />);
    expect(track?.getAttribute("data-checked")).toBe("true");
  });
});
