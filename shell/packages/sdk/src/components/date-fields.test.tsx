import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DateField, DateTimeField, TimeField } from "./date-fields.js";

afterEach(cleanup);

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// Mirrors date-fields.tsx's own local-time formatting, so this test stays
// correct regardless of the machine's timezone.
function expectedDateTimeInputValue(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}T${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

describe("DateField", () => {
  it("formats a Date value as YYYY-MM-DD", () => {
    render(<DateField label="Due" value={new Date("2026-03-05T00:00:00.000Z")} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Due") as HTMLInputElement).value).toBe("2026-03-05");
  });

  it("renders empty when value is undefined", () => {
    render(<DateField label="Due" value={undefined} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Due") as HTMLInputElement).value).toBe("");
  });

  it("normalizes a Date `min`/`max` to the input's YYYY-MM-DD format", () => {
    render(
      <DateField
        label="Due"
        value={undefined}
        onChange={vi.fn()}
        min={new Date("2026-03-05T00:00:00.000Z")}
        max={new Date("2026-12-31T00:00:00.000Z")}
      />,
    );
    const input = screen.getByLabelText("Due") as HTMLInputElement;
    expect(input.getAttribute("min")).toBe("2026-03-05");
    expect(input.getAttribute("max")).toBe("2026-12-31");
  });

  it("calls onChange with a Date on selection", () => {
    const onChange = vi.fn();
    render(<DateField label="Due" value={new Date("2026-03-05T00:00:00.000Z")} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Due"), { target: { value: "2026-04-01" } });
    expect(onChange).toHaveBeenCalledWith(new Date("2026-04-01"));
  });

  it("calls onChange with undefined when cleared", () => {
    const onChange = vi.fn();
    render(<DateField label="Due" value={new Date("2026-03-05T00:00:00.000Z")} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Due"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});

describe("DateTimeField", () => {
  it("renders a datetime-local input distinct from DateField", () => {
    render(<DateTimeField label="Starts" value={undefined} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Starts") as HTMLInputElement).type).toBe("datetime-local");
  });

  it("formats a Date value using local time components, matching how the input parses it back", () => {
    const value = new Date("2026-03-05T10:30:00.000Z");
    render(<DateTimeField label="Starts" value={value} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Starts") as HTMLInputElement).value).toBe(expectedDateTimeInputValue(value));
  });

  it("round-trips the displayed value back to the same instant, regardless of local timezone", () => {
    // The displayed string has no timezone of its own — parsing it back
    // (as the browser's native picker does on selection) must land on the
    // same instant it was formatted from, not shift by the local UTC offset.
    const original = new Date("2026-03-05T10:30:00.000Z");
    const displayed = expectedDateTimeInputValue(original);
    expect(new Date(displayed).getTime()).toBe(original.getTime());
  });

  it("calls onChange with a Date reflecting the newly selected local wall-clock time", () => {
    const onChange = vi.fn();
    render(<DateTimeField label="Starts" value={new Date("2026-03-05T10:30:00.000Z")} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Starts"), { target: { value: "2026-04-01T09:00" } });
    expect(onChange).toHaveBeenCalledWith(new Date("2026-04-01T09:00"));
  });

  it("calls onChange with undefined when cleared", () => {
    const onChange = vi.fn();
    render(<DateTimeField label="Starts" value={new Date("2026-03-05T10:30:00.000Z")} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Starts"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});

describe("TimeField", () => {
  it("renders a time input distinct from DateField/DateTimeField", () => {
    render(<TimeField label="Alarm" value={undefined} onChange={vi.fn()} />);
    expect((screen.getByLabelText("Alarm") as HTMLInputElement).type).toBe("time");
  });

  it("calls onChange with the raw string value", () => {
    const onChange = vi.fn();
    render(<TimeField label="Alarm" value="09:00" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Alarm"), { target: { value: "17:30" } });
    expect(onChange).toHaveBeenCalledWith("17:30");
  });

  it("applies min/max", () => {
    render(<TimeField label="Alarm" value="09:00" onChange={vi.fn()} min="08:00" max="18:00" />);
    const input = screen.getByLabelText("Alarm") as HTMLInputElement;
    expect(input.min).toBe("08:00");
    expect(input.max).toBe("18:00");
  });
});
