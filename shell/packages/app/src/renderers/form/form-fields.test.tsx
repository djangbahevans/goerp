import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FormFieldRow } from "./form-fields.js";
import type { FormField } from "./form-view-types.js";

afterEach(() => cleanup());

function withFieldAccess(access: Record<string, { read: boolean; write: boolean }>) {
  const value = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: { "contacts.contact": access },
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

const field: FormField = { field: "email", label: "Email", type: "email" };

describe("FormFieldRow", () => {
  it("renders nothing for a field the user lacks read access to", () => {
    const Wrapper = withFieldAccess({});
    const { container } = render(
      <Wrapper>
        <FormFieldRow field={field} resource="contacts.contact" record={{}} onChange={vi.fn()} formReadonly={false} />
      </Wrapper>,
    );
    expect(container.textContent).toBe("");
  });

  it("renders nothing for a field marked hidden, even with read access", () => {
    const Wrapper = withFieldAccess({ email: { read: true, write: true } });
    const { container } = render(
      <Wrapper>
        <FormFieldRow
          field={{ ...field, hidden: true }}
          resource="contacts.contact"
          record={{}}
          onChange={vi.fn()}
          formReadonly={false}
        />
      </Wrapper>,
    );
    expect(container.textContent).toBe("");
  });

  it("renders a disabled control when the user can read but not write", () => {
    const Wrapper = withFieldAccess({ email: { read: true, write: false } });
    render(
      <Wrapper>
        <FormFieldRow
          field={field}
          resource="contacts.contact"
          record={{ email: "a@b.com" }}
          onChange={vi.fn()}
          formReadonly={false}
        />
      </Wrapper>,
    );
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(true);
  });

  it("renders an editable control and reports edits when the user can write", () => {
    const Wrapper = withFieldAccess({ email: { read: true, write: true } });
    const onChange = vi.fn();
    render(
      <Wrapper>
        <FormFieldRow
          field={field}
          resource="contacts.contact"
          record={{ email: "a@b.com" }}
          onChange={onChange}
          formReadonly={false}
        />
      </Wrapper>,
    );
    const input = screen.getByDisplayValue("a@b.com") as HTMLInputElement;
    expect(input.disabled).toBe(false);
    fireEvent.change(input, { target: { value: "c@d.com" } });
    expect(onChange).toHaveBeenCalledWith({ email: "c@d.com" });
  });

  it("forces a readonly control when the whole form is readonly, even with write access", () => {
    const Wrapper = withFieldAccess({ email: { read: true, write: true } });
    render(
      <Wrapper>
        <FormFieldRow
          field={field}
          resource="contacts.contact"
          record={{ email: "a@b.com" }}
          onChange={vi.fn()}
          formReadonly
        />
      </Wrapper>,
    );
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(true);
  });
});
