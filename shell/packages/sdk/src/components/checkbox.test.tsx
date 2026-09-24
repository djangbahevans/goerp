import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Checkbox } from "./checkbox.js";
import { TextLink } from "./text-link.js";

afterEach(cleanup);

function box(): HTMLElement {
  return screen.getByRole("checkbox").nextElementSibling as HTMLElement;
}

describe("Checkbox", () => {
  it("is named by its visible label and toggles from a click on the label", () => {
    const onChange = vi.fn();
    render(<Checkbox label="Remember this device" checked={false} onChange={onChange} />);
    fireEvent.click(screen.getByText("Remember this device"));
    expect(onChange).toHaveBeenCalledWith(true);
    expect(screen.getByRole("checkbox", { name: "Remember this device" })).toBeTruthy();
  });

  it("reports false when a checked box is clicked", () => {
    const onChange = vi.fn();
    render(<Checkbox label="Saved" checked onChange={onChange} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(onChange).toHaveBeenCalledWith(false);
  });

  it("keeps a hidden label as the accessible name", () => {
    render(<Checkbox label="Select row" labelHidden checked={false} onChange={vi.fn()} />);
    expect(screen.getByText("Select row").className).toContain("sr-only");
    expect(screen.getByRole("checkbox", { name: "Select row" })).toBeTruthy();
  });

  it("indeterminate: sets the DOM property and aria-checked=mixed, shows a dash, and reports true on click", () => {
    const onChange = vi.fn();
    const { rerender } = render(<Checkbox label="Select all" checked={false} indeterminate onChange={onChange} />);
    const input = screen.getByRole("checkbox") as HTMLInputElement;
    expect(input.indeterminate).toBe(true);
    expect(input.getAttribute("aria-checked")).toBe("mixed");
    expect(box().dataset.state).toBe("indeterminate");
    expect(box().querySelector(".lucide-minus")).not.toBeNull();

    fireEvent.click(input);
    expect(onChange).toHaveBeenCalledWith(true);
    expect(input.indeterminate).toBe(true);

    rerender(<Checkbox label="Select all" checked indeterminate={false} onChange={onChange} />);
    expect(input.indeterminate).toBe(false);
    expect(input.hasAttribute("aria-checked")).toBe(false);
    expect(box().querySelector(".lucide-check")).not.toBeNull();
  });

  it("reports true when an indeterminate box that is also checked is clicked", () => {
    const onChange = vi.fn();
    render(<Checkbox label="Select all" checked indeterminate onChange={onChange} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("links the description and error, and marks the input invalid", () => {
    render(
      <Checkbox
        label="I agree"
        description="Required to continue"
        error="Accept the terms"
        checked={false}
        onChange={vi.fn()}
      />,
    );
    const input = screen.getByRole("checkbox");
    const describedBy = input.getAttribute("aria-describedby")?.split(" ");
    expect(describedBy).toEqual([screen.getByText("Required to continue").id, screen.getByRole("alert").id]);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(box().className).toContain("border-danger");
  });

  it("draws an empty box with the control-boundary border", () => {
    render(<Checkbox label="Saved" checked={false} onChange={vi.fn()} />);
    expect(box().className).toContain("border-border-control");
    expect(box().className).toContain("bg-surface");
  });

  it("disabled: disables the input and drops the hover highlight", () => {
    const onChange = vi.fn();
    render(<Checkbox label="Saved" checked={false} disabled onChange={onChange} />);
    const input = screen.getByRole("checkbox") as HTMLInputElement;
    expect(input.disabled).toBe(true);
    expect(box().className).not.toContain("group-hover:");
  });

  it("follows a TextLink inside the label without toggling the box", () => {
    const onChange = vi.fn();
    render(
      <Checkbox
        label={
          <>
            I agree to the <TextLink href="#terms">terms</TextLink>
          </>
        }
        checked={false}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("link", { name: "terms" }));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("passes name, value, id and ref to the native input", () => {
    const ref = createRef<HTMLInputElement>();
    render(<Checkbox ref={ref} id="terms" name="terms" value="yes" label="I agree" checked onChange={vi.fn()} />);
    const input = screen.getByRole("checkbox") as HTMLInputElement;
    expect(ref.current).toBe(input);
    expect(input.id).toBe("terms");
    expect(input.name).toBe("terms");
    expect(input.value).toBe("yes");
  });
});
