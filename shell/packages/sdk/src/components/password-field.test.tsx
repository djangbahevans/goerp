import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PasswordField } from "./password-field.js";

afterEach(cleanup);

describe("PasswordField", () => {
  it("renders a masked input by default", () => {
    render(<PasswordField label="Password" value="hunter2" autoComplete="current-password" onChange={vi.fn()} />);
    const input = screen.getByLabelText("Password") as HTMLInputElement;
    expect(input.type).toBe("password");
    expect(input.autocomplete).toBe("current-password");
  });

  it("toggles the native input type without clearing the value or moving focus", () => {
    render(<PasswordField label="Password" value="hunter2" autoComplete="current-password" onChange={vi.fn()} />);
    const input = screen.getByLabelText("Password") as HTMLInputElement;
    input.focus();

    fireEvent.click(screen.getByRole("button", { name: "Show password" }));

    expect(input.type).toBe("text");
    expect(input.value).toBe("hunter2");
    expect(document.activeElement).toBe(input);
    expect(screen.getByRole("button", { name: "Hide password" })).toBeTruthy();
  });

  it("calls onChange with the typed value", () => {
    const onChange = vi.fn();
    render(<PasswordField label="Password" value="" autoComplete="new-password" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "s3cret" } });
    expect(onChange).toHaveBeenCalledWith("s3cret");
  });

  it("surfaces the error message with role=alert", () => {
    render(
      <PasswordField
        label="Password"
        value=""
        autoComplete="current-password"
        onChange={vi.fn()}
        error="Invalid email or password"
      />,
    );
    expect(screen.getByRole("alert").textContent).toBe("Invalid email or password");
    expect(screen.getByLabelText("Password").getAttribute("aria-invalid")).toBe("true");
  });

  it("disables both the input and the toggle button", () => {
    render(
      <PasswordField label="Password" value="hunter2" autoComplete="current-password" onChange={vi.fn()} disabled />,
    );
    expect((screen.getByLabelText("Password") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Show password" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
