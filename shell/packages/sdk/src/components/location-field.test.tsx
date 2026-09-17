import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LocationField } from "./location-field.js";

afterEach(cleanup);

// jsdom has no WebGL context — the map itself is verified via a real
// browser (Storybook + Playwright), the same posture avatar-crop.test.tsx
// takes for its own canvas usage. The coordinate inputs are the actual
// accessible interface (docs/components/location-field.md's Accessibility
// section) and are what's tested here.
describe("LocationField", () => {
  it("renders latitude and longitude inputs with the field label composed in", () => {
    render(<LocationField label="Site location" value={{ lat: 5.56, lng: -0.2 }} onChange={vi.fn()} />);
    expect((screen.getByRole("spinbutton", { name: "Site location Latitude" }) as HTMLInputElement).value).toBe("5.56");
    expect((screen.getByRole("spinbutton", { name: "Site location Longitude" }) as HTMLInputElement).value).toBe(
      "-0.2",
    );
  });

  it("renders empty coordinate inputs when value is undefined", () => {
    render(<LocationField label="Site location" onChange={vi.fn()} />);
    expect((screen.getByRole("spinbutton", { name: "Site location Latitude" }) as HTMLInputElement).value).toBe("");
    expect((screen.getByRole("spinbutton", { name: "Site location Longitude" }) as HTMLInputElement).value).toBe("");
  });

  it("calls onChange with the edited axis, clamped to a valid range", () => {
    const onChange = vi.fn();
    render(<LocationField label="Site location" value={{ lat: 5.56, lng: -0.2 }} onChange={onChange} />);
    fireEvent.change(screen.getByRole("spinbutton", { name: "Site location Latitude" }), {
      target: { value: "999" },
    });
    expect(onChange).toHaveBeenCalledWith({ lat: 90, lng: -0.2 });
  });

  it("clamps longitude to [-180, 180]", () => {
    const onChange = vi.fn();
    render(<LocationField label="Site location" value={{ lat: 5.56, lng: -0.2 }} onChange={onChange} />);
    fireEvent.change(screen.getByRole("spinbutton", { name: "Site location Longitude" }), {
      target: { value: "-200" },
    });
    expect(onChange).toHaveBeenCalledWith({ lat: 5.56, lng: -180 });
  });

  it("disables both coordinate inputs when disabled", () => {
    render(<LocationField label="Site location" value={{ lat: 5.56, lng: -0.2 }} onChange={vi.fn()} disabled />);
    expect((screen.getByRole("spinbutton", { name: "Site location Latitude" }) as HTMLInputElement).disabled).toBe(
      true,
    );
    expect((screen.getByRole("spinbutton", { name: "Site location Longitude" }) as HTMLInputElement).disabled).toBe(
      true,
    );
  });

  it("shows the error message and marks both inputs invalid", () => {
    render(
      <LocationField
        label="Site location"
        value={{ lat: 5.56, lng: -0.2 }}
        onChange={vi.fn()}
        error="Location is required"
      />,
    );
    expect(screen.getByRole("alert").textContent).toBe("Location is required");
    expect(screen.getByRole("spinbutton", { name: "Site location Latitude" }).getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect(screen.getByRole("spinbutton", { name: "Site location Longitude" }).getAttribute("aria-invalid")).toBe(
      "true",
    );
  });
});
