import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PasswordStrengthMeter } from "./password-strength-meter.js";

afterEach(cleanup);

describe("PasswordStrengthMeter", () => {
  it("renders at rest with no score label for an empty password", () => {
    render(<PasswordStrengthMeter password="" onValidityChange={vi.fn()} />);
    expect(screen.queryByText("Weak")).toBeNull();
    expect(screen.queryByText("Fair")).toBeNull();
    expect(screen.queryByText("Good")).toBeNull();
    expect(screen.queryByText("Strong")).toBeNull();
  });

  it("refuses a long but low-entropy password despite meeting the length floor", () => {
    const onValidityChange = vi.fn();
    // Repeated characters score 0 in zxcvbn regardless of length.
    render(<PasswordStrengthMeter password={"a".repeat(20)} minLength={12} onValidityChange={onValidityChange} />);
    expect(onValidityChange).toHaveBeenLastCalledWith(false);
  });

  it("refuses a short but high-entropy password for failing the length floor", () => {
    const onValidityChange = vi.fn();
    render(<PasswordStrengthMeter password="X7$qzM!2" minLength={12} onValidityChange={onValidityChange} />);
    expect(onValidityChange).toHaveBeenLastCalledWith(false);
  });

  it("accepts a password meeting both the length floor and the score-2 gate", () => {
    const onValidityChange = vi.fn();
    render(
      <PasswordStrengthMeter password="correcthorsebatterystaple" minLength={12} onValidityChange={onValidityChange} />,
    );
    expect(onValidityChange).toHaveBeenLastCalledWith(true);
  });

  it("marks the length requirement satisfied once minLength is reached", () => {
    render(<PasswordStrengthMeter password="correcthorsebatterystaple" minLength={12} />);
    expect(screen.getByText("✓ At least 12 characters")).toBeTruthy();
  });

  it("leaves the length requirement unsatisfied below minLength", () => {
    render(<PasswordStrengthMeter password="short" minLength={12} />);
    expect(screen.getByText("At least 12 characters")).toBeTruthy();
  });

  it("only calls onValidityChange again when the combined validity actually flips", () => {
    const onValidityChange = vi.fn();
    const { rerender } = render(
      <PasswordStrengthMeter password="correcthorsebatterystaple" minLength={12} onValidityChange={onValidityChange} />,
    );
    expect(onValidityChange).toHaveBeenCalledTimes(1);

    // Still valid — appending a character shouldn't flip validity again.
    rerender(
      <PasswordStrengthMeter
        password="correcthorsebatterystaple!"
        minLength={12}
        onValidityChange={onValidityChange}
      />,
    );
    expect(onValidityChange).toHaveBeenCalledTimes(1);
  });

  it("hides the four-segment bar from assistive tech", () => {
    const { container } = render(<PasswordStrengthMeter password="correcthorsebatterystaple" />);
    expect(container.querySelector('[aria-hidden="true"]')).toBeTruthy();
  });

  it("fills exactly 1 segment for a score-0 (weak) password, not score+1", () => {
    // Repeated characters score 0 in zxcvbn.
    const { container } = render(<PasswordStrengthMeter password="aaaaaaaaaaaa" />);
    const bar = container.querySelector('[aria-hidden="true"]');
    expect(bar?.querySelectorAll(".bg-danger")).toHaveLength(1);
  });

  it("fills exactly 2 segments for a score-2 (fair) password", () => {
    const { container } = render(<PasswordStrengthMeter password="sunshine2024!" />);
    const bar = container.querySelector('[aria-hidden="true"]');
    expect(bar?.querySelectorAll(".bg-warning")).toHaveLength(2);
  });

  it("exposes a single aria-live=polite status region", () => {
    render(<PasswordStrengthMeter password="correcthorsebatterystaple" />);
    const status = screen.getByRole("status");
    expect(status.getAttribute("aria-live")).toBe("polite");
  });

  it("does not change the status text on every keystroke while the score bucket and length-satisfied state stay the same", () => {
    const { rerender } = render(<PasswordStrengthMeter password="correcthorsebatterystaple" minLength={12} />);
    const before = screen.getByRole("status").textContent;

    rerender(<PasswordStrengthMeter password="correcthorsebatterystaple!" minLength={12} />);

    expect(screen.getByRole("status").textContent).toBe(before);
  });

  it("changes the status text when the score bucket changes", () => {
    const { rerender } = render(<PasswordStrengthMeter password="aaaaaaaaaaaa" minLength={12} />);
    const before = screen.getByRole("status").textContent;

    rerender(<PasswordStrengthMeter password="correcthorsebatterystaple" minLength={12} />);

    expect(screen.getByRole("status").textContent).not.toBe(before);
  });
});
