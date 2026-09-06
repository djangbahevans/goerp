import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SegmentedField } from "./segmented-field.js";

afterEach(cleanup);

const options = [
  { value: "person", label: "Person" },
  { value: "company", label: "Company" },
];

describe("SegmentedField", () => {
  it("renders every option", () => {
    render(<SegmentedField options={options} value="person" onChange={() => {}} />);
    expect(screen.getByRole("radio", { name: "Person" })).toBeTruthy();
    expect(screen.getByRole("radio", { name: "Company" })).toBeTruthy();
  });

  it("marks the selected option as checked", () => {
    render(<SegmentedField options={options} value="company" onChange={() => {}} />);
    expect((screen.getByRole("radio", { name: "Company" }) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByRole("radio", { name: "Person" }) as HTMLInputElement).checked).toBe(false);
  });

  it("calls onChange with the clicked option's value", () => {
    const onChange = vi.fn();
    render(<SegmentedField options={options} value="person" onChange={onChange} />);
    fireEvent.click(screen.getByRole("radio", { name: "Company" }));
    expect(onChange).toHaveBeenCalledWith("company");
  });

  it("disables every option when disabled", () => {
    render(<SegmentedField options={options} value="person" onChange={() => {}} disabled />);
    expect(screen.getByRole("radio", { name: "Person" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("radio", { name: "Company" }).hasAttribute("disabled")).toBe(true);
  });

  it("disables an individual option", () => {
    const disabledOptions = [
      { value: "person", label: "Person" },
      { value: "company", label: "Company", disabled: true },
    ];
    render(<SegmentedField options={disabledOptions} value="person" onChange={() => {}} />);
    expect(screen.getByRole("radio", { name: "Company" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("radio", { name: "Person" }).hasAttribute("disabled")).toBe(false);
  });
});
