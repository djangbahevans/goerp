import { act, cleanup, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { type ReactNode, StrictMode, type SubmitEvent } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FieldWrapper } from "../components/field-wrapper.js";
import { TextInput } from "../components/text-input.js";
import { useForm } from "./index.js";

const defaultValues = { name: "", email: "", count: 0 };

function submitEvent() {
  return { preventDefault: vi.fn() } as unknown as SubmitEvent<HTMLFormElement>;
}

afterEach(cleanup);

describe("useForm", () => {
  it("binds value-based controls and displays server errors through FieldWrapper", () => {
    function Form() {
      const form = useForm({ defaultValues });
      return (
        <form>
          <FieldWrapper label="Name" error={form.errors.name}>
            <TextInput {...form.register("name")} />
          </FieldWrapper>
          <button type="button" onClick={() => form.setErrors({ name: ["Name already exists", "Use another"] })}>
            Server error
          </button>
          <output>{JSON.stringify({ values: form.values, touched: form.touched, dirty: form.isDirty })}</output>
        </form>
      );
    }
    render(<Form />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ama" } });
    fireEvent.blur(screen.getByLabelText("Name"));
    expect(screen.getByRole("status").textContent).toContain('"name":"Ama"');
    expect(screen.getByRole("status").textContent).toContain('"name":true');
    expect(screen.getByRole("status").textContent).toContain('"dirty":true');
    fireEvent.click(screen.getByText("Server error"));
    expect(screen.getByText("Name already exists")).toBeDefined();
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBe("true");
  });

  it("validates on submit and changes after touch, preserving untouched field errors", () => {
    const validate = vi.fn((values: typeof defaultValues) => ({
      ...(values.name ? {} : { name: "Required" }),
      ...(values.email.includes("@") ? {} : { email: "Invalid email" }),
    }));
    const { result } = renderHook(() => useForm({ defaultValues, validate }));
    expect(result.current.isValid).toBe(true);
    act(() => result.current.setValue("name", "Ama"));
    expect(validate).not.toHaveBeenCalled();
    act(() => result.current.register("name").onBlur());
    expect(validate).not.toHaveBeenCalled();
    act(() => result.current.setErrors({ email: ["Reserved email"] }));
    act(() => result.current.register("name").onChange(""));
    expect(result.current.errors).toEqual({ name: "Required", email: "Reserved email" });
    expect(result.current.isValid).toBe(false);

    const fn = vi.fn();
    const event = submitEvent();
    act(() => result.current.handleSubmit(fn)(event));
    expect(event.preventDefault).toHaveBeenCalledOnce();
    expect(fn).not.toHaveBeenCalled();
    expect(result.current.errors).toEqual({ name: "Required", email: "Invalid email" });
    expect(result.current.touched).toEqual({ name: true, email: true, count: true });
    act(() => result.current.setValues({ name: "Ama", email: "ama@example.test" }));
    expect(result.current.errors).toEqual({});
    act(() => result.current.handleSubmit(fn)(submitEvent()));
    expect(fn).toHaveBeenCalledWith({ name: "Ama", email: "ama@example.test", count: 0 });
  });

  it("merges known server fields, ignores unknown and inherited keys, and clears errors", () => {
    const { result } = renderHook(() => useForm({ defaultValues }));
    act(() => result.current.setErrors({ name: ["First", "Second"], other: ["Unknown"], toString: ["Unknown"] }));
    act(() => result.current.setErrors({ email: [""], count: [] }));
    expect(result.current.errors).toEqual({ name: "First", email: "" });
    expect(result.current.isValid).toBe(false);
    act(() => result.current.setErrors(Object.create({ name: ["Inherited"] }) as Record<string, string[]>));
    expect(result.current.errors.name).toBe("First");
    act(() => result.current.clearError("email"));
    act(() => result.current.setValue("name", "Ama"));
    expect(result.current.errors).toEqual({});
    expect(result.current.isValid).toBe(true);
  });

  it("treats empty validation messages as errors and refreshes touched cross-field rules", () => {
    const { result } = renderHook(() =>
      useForm({
        defaultValues,
        validate: (values) => ({ ...(values.name === values.email ? {} : { email: "Must match" }), name: "" }),
      }),
    );
    const fn = vi.fn();
    act(() => result.current.handleSubmit(fn)(submitEvent()));
    expect(result.current.isValid).toBe(false);
    expect(fn).not.toHaveBeenCalled();
    act(() => result.current.setValue("name", "Ama"));
    expect(result.current.errors.email).toBe("Must match");
    act(() => result.current.setValue("email", "Ama"));
    expect(result.current.errors).toEqual({ name: "" });
  });

  it("reads consecutive edits and submission in the same batch under StrictMode", () => {
    const wrapper = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>;
    const { result } = renderHook(() => useForm({ defaultValues }), { wrapper });
    const fn = vi.fn();
    act(() => {
      result.current.setValue("name", "Ama");
      result.current.setValues({ email: "ama@example.test", count: 2 });
      result.current.handleSubmit(fn)(submitEvent());
    });
    expect(fn).toHaveBeenCalledOnce();
    expect(fn).toHaveBeenCalledWith({ name: "Ama", email: "ama@example.test", count: 2 });
    expect(result.current.values).toEqual(fn.mock.calls[0]?.[0]);
  });

  it("restores mount defaults or supplied values and establishes a clean baseline", () => {
    const { result, rerender } = renderHook(({ defaults }) => useForm({ defaultValues: defaults }), {
      initialProps: { defaults: defaultValues },
    });
    act(() => result.current.setValue("name", "Ama"));
    expect(result.current.isDirty).toBe(true);
    act(() => result.current.setValue("name", ""));
    expect(result.current.isDirty).toBe(false);
    act(() => result.current.register("name").onBlur());
    act(() => result.current.setErrors({ name: ["Required"] }));
    const loaded = { name: "Kofi", email: "kofi@example.test", count: 2 };
    act(() => result.current.reset(loaded));
    expect(result.current.values).toEqual(loaded);
    expect(result.current.isDirty).toBe(false);
    expect(result.current.isValid).toBe(true);
    expect(result.current.touched).toEqual({});
    act(() => result.current.setValue("count", 3));
    expect(result.current.isDirty).toBe(true);
    rerender({ defaults: loaded });
    act(() => result.current.reset());
    expect(result.current.values).toEqual(defaultValues);
    expect(result.current.isDirty).toBe(false);
    expect(defaultValues).toEqual({ name: "", email: "", count: 0 });
  });

  it("does not treat inherited property names as touched fields", () => {
    const validate = vi.fn(() => ({}));
    const { result } = renderHook(() => useForm({ defaultValues: { toString: "" }, validate }));
    act(() => result.current.setValue("toString", "Ama"));
    expect(validate).not.toHaveBeenCalled();
    act(() => result.current.register("toString").onBlur());
    act(() => result.current.setValue("toString", "Kofi"));
    expect(validate).toHaveBeenCalledOnce();
    expect(result.current.errors).toEqual({});
  });

  it("blocks submission on current errors without a validator", () => {
    const { result } = renderHook(() => useForm({ defaultValues }));
    const fn = vi.fn();
    act(() => result.current.setErrors({ name: ["Reserved name"] }));
    act(() => result.current.handleSubmit(fn)(submitEvent()));
    expect(fn).not.toHaveBeenCalled();
    act(() => result.current.clearError("name"));
    act(() => result.current.handleSubmit(fn)(submitEvent()));
    expect(fn).toHaveBeenCalledOnce();
  });

  it("uses the latest validator after rerender and isolates controllers", () => {
    const { result, rerender } = renderHook(
      ({ required }) => ({
        first: useForm({ defaultValues, validate: () => (required ? { name: "Required" } : {}) }),
        second: useForm({ defaultValues }),
      }),
      { initialProps: { required: false } },
    );
    rerender({ required: true });
    const fn = vi.fn();
    act(() => result.current.first.handleSubmit(fn)(submitEvent()));
    expect(fn).not.toHaveBeenCalled();
    expect(result.current.second.errors).toEqual({});
    expect(result.current.second.touched).toEqual({});
  });
});
