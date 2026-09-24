import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Button } from "./button.js";
import { DateField, TimeField } from "./date-fields.js";
import { fieldInputClassName } from "./field-input-styles.js";
import { Select } from "./select.js";
import { TextArea } from "./text-input.js";

afterEach(cleanup);

describe("fieldInputClassName", () => {
  it("gives a single-line control Button's fixed heights", () => {
    expect(fieldInputClassName(false)).toContain("h-9");
    expect(fieldInputClassName(false, "input", "sans", "sm")).toContain("h-7");
  });

  it("leaves multi-line controls and editor wrappers sized by padding", () => {
    for (const className of [
      fieldInputClassName(false, "wrapper"),
      fieldInputClassName(false, "input", "sans", "auto"),
    ]) {
      expect(className).not.toMatch(/\bh-\d/);
      expect(className).toContain("py-2");
    }
  });

  it("lines up the date, time and select controls with an md Button", () => {
    render(
      <>
        <DateField aria-label="Due" value={undefined} onChange={() => {}} />
        <TimeField label="At" value={undefined} onChange={() => {}} />
        <Select options={[{ value: "a", label: "A" }]} value="" onChange={() => {}} />
        <Button>Apply</Button>
      </>,
    );
    for (const control of [
      screen.getByLabelText("Due"),
      screen.getByLabelText("At"),
      screen.getByRole("combobox"),
      screen.getByRole("button", { name: "Apply" }),
    ]) {
      expect(control.className).toMatch(/\bh-9\b/);
    }
  });

  it("keeps TextArea's height padding-derived", () => {
    render(<TextArea aria-label="Notes" value="" onChange={() => {}} />);
    expect(screen.getByLabelText("Notes").className).not.toMatch(/\bh-9\b/);
  });
});
