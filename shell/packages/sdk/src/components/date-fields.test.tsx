import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DateField, DateTimeField, TimeField } from "./date-fields.js";

afterEach(cleanup);

describe("DateField", () => {
  it("normalizes a Date `min` to the input's YYYY-MM-DD format", () => {
    render(<DateField label="Due" value={undefined} onChange={vi.fn()} min={new Date("2026-03-05T00:00:00.000Z")} />);
    expect((screen.getByLabelText("Due") as HTMLInputElement).getAttribute("min")).toBe("2026-03-05");
  });

  it("passes a string `min`/`max` through as ISO date slices", () => {
    render(
      <DateField label="Due" value={undefined} onChange={vi.fn()} min="2026-01-01" max="2026-12-31T00:00:00.000Z" />,
    );
    const input = screen.getByLabelText("Due") as HTMLInputElement;
    expect(input.getAttribute("min")).toBe("2026-01-01");
    expect(input.getAttribute("max")).toBe("2026-12-31");
  });

  it("calls onChange with the input value, or undefined when cleared", () => {
    const onChange = vi.fn();
    render(<DateField label="Due" value="2026-03-05" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Due"), { target: { value: "2026-04-01" } });
    expect(onChange).toHaveBeenCalledWith("2026-04-01");
    fireEvent.change(screen.getByLabelText("Due"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});

describe("DateTimeField", () => {
  it("renders a datetime-local input distinct from DateField", () => {
    render(<DateTimeField label="Starts" value={undefined} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Starts") as HTMLInputElement).type).toBe("datetime-local");
  });
});

describe("TimeField", () => {
  it("renders a time input distinct from DateField/DateTimeField", () => {
    render(<TimeField label="Alarm" value={undefined} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Alarm") as HTMLInputElement).type).toBe("time");
  });
});
