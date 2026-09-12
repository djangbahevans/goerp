import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SliderField } from "./slider-field.js";

afterEach(cleanup);

describe("SliderField", () => {
  it("renders a range input at the given value", () => {
    render(<SliderField value={30} min={0} max={100} step={1} onChange={vi.fn()} />);
    const slider = screen.getByRole("slider") as HTMLInputElement;
    expect(slider.value).toBe("30");
    expect(slider.min).toBe("0");
    expect(slider.max).toBe("100");
    expect(slider.step).toBe("1");
  });

  it("reports the new value on change", () => {
    const onChange = vi.fn();
    render(<SliderField value={30} min={0} max={100} step={1} onChange={onChange} />);
    fireEvent.change(screen.getByRole("slider"), { target: { value: "70" } });
    expect(onChange).toHaveBeenCalledWith(70);
  });

  it("is disabled and non-interactive when disabled", () => {
    render(<SliderField value={30} min={0} max={100} step={1} onChange={vi.fn()} disabled />);
    const slider = screen.getByRole("slider") as HTMLInputElement;
    expect(slider.disabled).toBe(true);
  });

  it("associates the numeric readout with the input via a native for/id pair", () => {
    render(<SliderField id="volume" value={30} min={0} max={100} step={1} onChange={vi.fn()} />);
    expect(screen.getByText("30").closest("output")?.getAttribute("for")).toBe("volume");
  });

  it("computes the fill percentage from value/min/max as a CSS custom property", () => {
    render(<SliderField value={25} min={0} max={100} step={1} onChange={vi.fn()} />);
    const slider = screen.getByRole("slider") as HTMLInputElement;
    expect(slider.style.getPropertyValue("--slider-fill")).toBe("25%");
  });
});
