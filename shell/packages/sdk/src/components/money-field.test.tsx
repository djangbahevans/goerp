import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MoneyField } from "./money-field.js";

afterEach(cleanup);

describe("MoneyField", () => {
  it("displays the value converted from integer minor units to the currency's major-unit decimal", () => {
    // 10000 GHS pesewas is displayed to the user as 100.00 cedis.
    render(<MoneyField label="Amount" value={10000} currency="GHS" onChange={vi.fn()} />);
    expect(screen.getByText("GHS")).toBeTruthy();
    expect((screen.getByRole("spinbutton") as HTMLInputElement).value).toBe("100");
  });

  it("displays a zero-decimal currency (e.g. XOF) without dividing", () => {
    render(<MoneyField label="Amount" value={10000} currency="XOF" onChange={vi.fn()} />);
    expect((screen.getByRole("spinbutton") as HTMLInputElement).value).toBe("10000");
  });

  it("calls onChange with the parsed major-unit input converted back to integer minor units", () => {
    const onChange = vi.fn();
    render(<MoneyField label="Amount" value={undefined} currency="GHS" onChange={onChange} />);
    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "250.5" } });
    expect(onChange).toHaveBeenCalledWith(25050);
  });

  it("calls onChange with undefined when the input is cleared", () => {
    const onChange = vi.fn();
    render(<MoneyField label="Amount" value={10000} currency="GHS" onChange={onChange} />);
    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(undefined);
  });

  it("surfaces the error message", () => {
    render(<MoneyField label="Amount" value={10000} currency="GHS" onChange={vi.fn()} error="Required" />);
    expect(screen.getByRole("alert").textContent).toBe("Required");
    expect(screen.getByRole("spinbutton").getAttribute("aria-invalid")).toBe("true");
  });

  it("accepts a dynamically resolved currency value, not just a static code", () => {
    const resolvedCurrency = ["GHS", "USD"][1] as string;
    render(<MoneyField label="Amount" value={10000} currency={resolvedCurrency} onChange={vi.fn()} />);
    expect(screen.getByText("USD")).toBeTruthy();
  });

  it("renders no currency badge when currency is omitted", () => {
    const { container } = render(<MoneyField label="Amount" value={10000} onChange={vi.fn()} />);
    expect(container.querySelectorAll("span span")).toHaveLength(0);
  });
});
