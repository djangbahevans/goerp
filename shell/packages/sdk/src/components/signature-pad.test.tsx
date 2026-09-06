import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SignaturePad } from "./signature-pad.js";

afterEach(cleanup);

describe("SignaturePad", () => {
  it("renders a blank canvas when there is no value", () => {
    const { container } = render(<SignaturePad value="" onChange={() => {}} />);
    expect(container.querySelector("canvas")).toBeTruthy();
    expect(container.querySelector("img")).toBeNull();
  });

  it("renders the captured signature as an image when a value is set", () => {
    render(<SignaturePad value="data:image/png;base64,abc" onChange={() => {}} />);
    const img = screen.getByAltText("Signature") as HTMLImageElement;
    expect(img.src).toBe("data:image/png;base64,abc");
  });

  it("clears the value when Clear is clicked", () => {
    const onChange = vi.fn();
    render(<SignaturePad value="data:image/png;base64,abc" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("disables the Clear button when disabled", () => {
    render(<SignaturePad value="data:image/png;base64,abc" onChange={() => {}} disabled />);
    expect(screen.getByRole("button", { name: "Clear" }).hasAttribute("disabled")).toBe(true);
  });
});
