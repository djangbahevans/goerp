import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sanitizeCode, VerificationCodeInput, type VerificationCodeInputProps } from "./verification-code-input.js";

afterEach(cleanup);

// A controlled harness, since the component owns no value state.
function Harness(props: Partial<VerificationCodeInputProps> & { initial?: string }) {
  const { initial = "", ...rest } = props;
  const [value, setValue] = useState(initial);
  return <VerificationCodeInput label="Verification code" value={value} onChange={setValue} {...rest} />;
}

function input(): HTMLInputElement {
  return screen.getByLabelText("Verification code") as HTMLInputElement;
}

describe("sanitizeCode", () => {
  it.each([
    ["123 456", "123456"],
    ["123-456", "123456"],
    ["\t123456\n", "123456"],
    ["123456789012", "123456"],
    ["12a4", "124"],
  ])("sanitizes %j to %j", (raw, want) => {
    expect(sanitizeCode(raw, 6)).toBe(want);
  });
});

describe("VerificationCodeInput", () => {
  it("renders a single labelled one-time-code field without maxlength", () => {
    render(<Harness />);
    const el = input();
    expect(el.type).toBe("text");
    expect(el.inputMode).toBe("numeric");
    expect(el.getAttribute("autocomplete")).toBe("one-time-code");
    expect(el.hasAttribute("maxlength")).toBe(false);
  });

  it("ignores non-digit keystrokes", () => {
    render(<Harness initial="12" />);
    fireEvent.change(input(), { target: { value: "12a" } });
    expect(input().value).toBe("12");
  });

  it("calls onComplete once when typing reaches six digits", () => {
    const onComplete = vi.fn();
    render(<Harness initial="12345" onComplete={onComplete} />);

    fireEvent.change(input(), { target: { value: "123456" } });
    fireEvent.change(input(), { target: { value: "1234567" } });

    expect(onComplete).toHaveBeenCalledTimes(1);
    expect(onComplete).toHaveBeenCalledWith("123456");
  });

  it("sanitizes and completes a pasted code with separators", () => {
    const onComplete = vi.fn();
    render(<Harness onComplete={onComplete} />);

    fireEvent.change(input(), { target: { value: "123-456" } });

    expect(input().value).toBe("123456");
    expect(onComplete).toHaveBeenCalledWith("123456");
  });

  it("keeps the first six digits of an over-long paste", () => {
    const onComplete = vi.fn();
    render(<Harness onComplete={onComplete} />);
    fireEvent.change(input(), { target: { value: "123456789012" } });
    expect(onComplete).toHaveBeenCalledWith("123456");
  });

  it("does not call onComplete for a complete value set through props", () => {
    const onComplete = vi.fn();
    const { rerender } = render(
      <VerificationCodeInput label="Verification code" value="" onChange={() => {}} onComplete={onComplete} />,
    );
    rerender(
      <VerificationCodeInput label="Verification code" value="123456" onChange={() => {}} onComplete={onComplete} />,
    );
    rerender(
      <VerificationCodeInput label="Verification code" value="123456" onChange={() => {}} onComplete={onComplete} />,
    );
    expect(onComplete).not.toHaveBeenCalled();
  });

  it("completes again after the user edits a completed code", () => {
    const onComplete = vi.fn();
    render(<Harness initial="123456" onComplete={onComplete} />);

    fireEvent.change(input(), { target: { value: "12345" } });
    fireEvent.change(input(), { target: { value: "123459" } });

    expect(onComplete).toHaveBeenCalledTimes(1);
    expect(onComplete).toHaveBeenCalledWith("123459");
  });

  it("honours a custom length", () => {
    const onComplete = vi.fn();
    render(<Harness length={8} onComplete={onComplete} />);
    fireEvent.change(input(), { target: { value: "123456" } });
    expect(onComplete).not.toHaveBeenCalled();
    fireEvent.change(input(), { target: { value: "12345678" } });
    expect(onComplete).toHaveBeenCalledWith("12345678");
  });

  it("wires description and error into aria-describedby with an alert", () => {
    render(<Harness description="Enter the 6-digit code from your authenticator app." error="Incorrect code" />);
    const el = input();
    const describedBy = el.getAttribute("aria-describedby")?.split(" ") ?? [];

    expect(describedBy).toHaveLength(2);
    expect(document.getElementById(describedBy[0] ?? "")?.textContent).toMatch(/authenticator app/);
    expect(document.getElementById(describedBy[1] ?? "")?.textContent).toBe("Incorrect code");
    expect(el.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole("alert").textContent).toBe("Incorrect code");
  });

  it("omits aria-describedby and aria-invalid when there is nothing to describe", () => {
    render(<Harness />);
    expect(input().hasAttribute("aria-describedby")).toBe(false);
    expect(input().hasAttribute("aria-invalid")).toBe(false);
  });

  it("applies the shake only while an error is shown, and only under motion-safe", () => {
    const { rerender } = render(<VerificationCodeInput label="Verification code" value="" onChange={() => {}} />);
    expect(input().className).not.toMatch(/verification-code-shake/);

    rerender(<VerificationCodeInput label="Verification code" value="" onChange={() => {}} error="Incorrect code" />);
    expect(input().className).toMatch(/motion-safe:animate-\[verification-code-shake/);
  });

  it("merges the size override over the shared input chrome", () => {
    render(<Harness error="Incorrect code" />);
    const classes = input().className.split(" ");
    expect(classes).toContain("text-3xl");
    expect(classes).not.toContain("text-sm");
    expect(classes).toContain("border-danger");
  });

  it("forwards its ref to the input", () => {
    const ref = createRef<HTMLInputElement>();
    render(<VerificationCodeInput label="Verification code" value="" onChange={() => {}} ref={ref} />);
    expect(ref.current).toBe(input());
  });

  it("focuses on mount when autoFocus is set", () => {
    render(<Harness autoFocus />);
    expect(document.activeElement).toBe(input());
  });
});
