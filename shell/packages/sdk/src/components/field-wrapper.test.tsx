import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FieldWrapper } from "./field-wrapper.js";

afterEach(cleanup);

describe("FieldWrapper", () => {
  it("renders the label and children", () => {
    render(
      <FieldWrapper label="Name">
        <input />
      </FieldWrapper>,
    );
    expect(screen.getByLabelText("Name")).toBeTruthy();
  });

  it("marks the field required", () => {
    render(
      <FieldWrapper label="Name" required>
        <input />
      </FieldWrapper>,
    );
    expect(screen.getByText("*")).toBeTruthy();
  });

  it("renders an error message", () => {
    render(
      <FieldWrapper label="Name" error="Name is required">
        <input />
      </FieldWrapper>,
    );
    expect(screen.getByRole("alert").textContent).toBe("Name is required");
  });

  it("renders no error message when omitted", () => {
    render(
      <FieldWrapper label="Name">
        <input />
      </FieldWrapper>,
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
