import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { DateField, DateTimeField, TimeField } from "./date-fields.js";
import { FieldWrapper, useFieldControl } from "./field-wrapper.js";
import { SegmentedField } from "./segmented-field.js";
import { TagsField } from "./tags-field.js";
import { TextInput } from "./text-input.js";
import { ToggleField } from "./toggle-field.js";

afterEach(cleanup);

function ContextProbe() {
  const field = useFieldControl();
  return <output data-testid="probe">{JSON.stringify(field)}</output>;
}

describe("FieldWrapper", () => {
  it("labels its control with a sibling <label htmlFor>", () => {
    render(
      <FieldWrapper label="Name">
        <TextInput value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const input = screen.getByLabelText("Name");
    expect(input.tagName).toBe("INPUT");
    expect(input.closest("label")).toBeNull();
  });

  it("marks the label and the control required", () => {
    render(
      <FieldWrapper label="Name" required>
        <TextInput value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    expect(screen.getByText("*").getAttribute("aria-hidden")).toBe("true");
    expect((screen.getByLabelText(/Name/) as HTMLInputElement).required).toBe(true);
  });

  it("marks the control invalid and links the error", () => {
    render(
      <FieldWrapper label="Name" error="Name is required">
        <TextInput value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toBe("Name is required");
    const input = screen.getByLabelText("Name");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toBe(alert.id);
  });

  it("describes the control with the description, then the error", () => {
    render(
      <FieldWrapper label="Email" description="We'll send a link here." error="Enter an email">
        <TextInput value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const describedBy = screen.getByLabelText("Email").getAttribute("aria-describedby")?.split(" ");
    expect(describedBy).toEqual([screen.getByText("We'll send a link here.").id, screen.getByRole("alert").id]);
  });

  it("leaves a valid control without aria-invalid or aria-describedby", () => {
    render(
      <FieldWrapper label="Name">
        <TextInput value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const input = screen.getByLabelText("Name");
    expect(input.hasAttribute("aria-invalid")).toBe(false);
    expect(input.hasAttribute("aria-describedby")).toBe(false);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("provides the wiring to a custom control through useFieldControl", () => {
    render(
      <FieldWrapper label="Status" error="Pick one" required>
        <ContextProbe />
      </FieldWrapper>,
    );
    const field = JSON.parse(screen.getByTestId("probe").textContent ?? "null");
    const label = screen.getByText("Status").closest("label");
    expect(field).toEqual({
      id: label?.getAttribute("for"),
      describedBy: screen.getByRole("alert").id,
      invalid: true,
      required: true,
    });
  });

  it("gives useFieldControl null outside a FieldWrapper", () => {
    render(<ContextProbe />);
    expect(screen.getByTestId("probe").textContent).toBe("null");
  });

  describe("wires the SDK's custom controls through FieldContext", () => {
    const noop = () => {};
    const controls: Array<[string, ReactNode]> = [
      ["DateField", <DateField key="d" value={undefined} onChange={noop} />],
      ["DateTimeField", <DateTimeField key="dt" value={undefined} onChange={noop} />],
      ["TimeField", <TimeField key="t" value={undefined} onChange={noop} />],
      ["TagsField", <TagsField key="tg" value={[]} options={[]} onChange={noop} />],
      ["ToggleField", <ToggleField key="tf" value={false} onChange={noop} />],
      [
        "SegmentedField",
        <SegmentedField
          key="s"
          value="b"
          options={[
            { value: "a", label: "A" },
            { value: "b", label: "B" },
          ]}
          onChange={noop}
        />,
      ],
    ];

    it.each(controls)("%s", (_, control) => {
      render(
        <FieldWrapper label="Field" error="Fix this" required>
          {control}
        </FieldWrapper>,
      );
      const labelled = screen.getByLabelText(/^Field/) as HTMLInputElement;
      expect(labelled.getAttribute("aria-invalid")).toBe("true");
      expect(labelled.getAttribute("aria-describedby")).toBe(screen.getByRole("alert").id);
      if (labelled.getAttribute("role") === "combobox") {
        expect(labelled.required).toBe(false);
        expect(labelled.getAttribute("aria-required")).toBe("true");
      } else {
        expect(labelled.required).toBe(true);
      }
    });
  });
});
