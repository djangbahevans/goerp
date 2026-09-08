import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FormFieldRow } from "./form-fields.js";
import type { FormField } from "./form-view-types.js";

// Only the label-association tests below (tags/relation-backed fields) need
// a real resource query — matches field-renderers.test.tsx's own mocking.
const { getMock } = vi.hoisted(() => ({
  getMock: vi.fn(async () => ({
    data: [{ id: "1", name: "VIP" }],
    meta: { cursor: null, hasMore: false },
  })),
}));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, resourceRegistry: { resolve: vi.fn(async () => ({ listPath: "/tags" })) } };
});

afterEach(() => cleanup());

function renderWithQueryClient(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

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

  // goerp#698: FormFieldRow used to wrap FieldInput's output in a bare
  // <label>, relying on the browser associating it with the first
  // labelable descendant — wrong whenever a field's own markup puts
  // something else labelable ahead of its actual primary control.
  describe("label association (goerp#698)", () => {
    it("associates the visible label with the field's actual input", () => {
      const Wrapper = withFieldAccess({ email: { read: true, write: true } });
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
      expect(screen.getByLabelText("Email")).toBe(screen.getByDisplayValue("a@b.com"));
    });

    it("boolean: clicking the visible label still toggles the checkbox via native htmlFor click-forwarding", () => {
      const Wrapper = withFieldAccess({ active: { read: true, write: true } });
      const onChange = vi.fn();
      render(
        <Wrapper>
          <FormFieldRow
            field={{ field: "active", type: "boolean", label: "Active" }}
            resource="contacts.contact"
            record={{ active: false }}
            onChange={onChange}
            formReadonly={false}
          />
        </Wrapper>,
      );
      fireEvent.click(screen.getByText("Active"));
      expect(onChange).toHaveBeenCalledWith({ active: true });
    });

    it("rich_text: associates the visible label via aria-labelledby, since the contentEditable primary control isn't natively labelable", () => {
      const Wrapper = withFieldAccess({ notes: { read: true, write: true } });
      render(
        <Wrapper>
          <FormFieldRow
            field={{ field: "notes", type: "rich_text", label: "Notes" }}
            resource="contacts.contact"
            record={{ notes: "<p>Hello</p>" }}
            onChange={vi.fn()}
            formReadonly={false}
          />
        </Wrapper>,
      );
      expect(screen.getByLabelText("Notes")).toBe(screen.getByRole("textbox"));
    });

    it("code: associates the visible label via aria-labelledby, since the contentEditable primary control isn't natively labelable", () => {
      const Wrapper = withFieldAccess({ script: { read: true, write: true } });
      render(
        <Wrapper>
          <FormFieldRow
            field={{ field: "script", type: "code", label: "Script" }}
            resource="contacts.contact"
            record={{ script: "print(1)" }}
            onChange={vi.fn()}
            formReadonly={false}
          />
        </Wrapper>,
      );
      expect(screen.getByLabelText("Script")).toBe(screen.getByRole("textbox"));
    });

    it("tags: associates the visible label with the combobox input, not a selected tag's remove button", async () => {
      const Wrapper = withFieldAccess({ tag_ids: { read: true, write: true } });
      renderWithQueryClient(
        <Wrapper>
          <FormFieldRow
            field={{
              field: "tag_ids",
              type: "tags",
              label: "Skills",
              resource: "contacts.tag",
              resource_label_field: "name",
            }}
            resource="contacts.contact"
            record={{ tags: [{ id: "1", name: "VIP" }] }}
            onChange={vi.fn()}
            formReadonly={false}
          />
        </Wrapper>,
      );
      expect(await screen.findByText("VIP")).toBeTruthy();
      // Before goerp#698's fix, the wrapping <label>'s first labelable
      // descendant was this pill's own "Remove tag" button, not the input.
      expect(screen.getByLabelText("Skills")).toBe(screen.getByRole("combobox"));
      expect(screen.getByLabelText("Skills")).not.toBe(screen.getByLabelText("Remove tag: VIP"));
    });

    it("signature: the visible label isn't wired to the Clear button, and the canvas gets its own accessible name", () => {
      const Wrapper = withFieldAccess({ sig: { read: true, write: true } });
      const onChange = vi.fn();
      render(
        <Wrapper>
          <FormFieldRow
            field={{ field: "sig", type: "signature", label: "Signature" }}
            resource="contacts.contact"
            record={{ sig: null }}
            onChange={onChange}
            formReadonly={false}
          />
        </Wrapper>,
      );
      // Before goerp#698's fix, the wrapping <label>'s first (and only)
      // labelable descendant was the Clear button, so clicking the field's
      // own label silently wiped the captured signature.
      fireEvent.click(screen.getByText("Signature"));
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByLabelText("Signature").tagName).toBe("CANVAS");
    });

    it("radio: the visible label doesn't nest inside each option's own native label", () => {
      const Wrapper = withFieldAccess({ priority: { read: true, write: true } });
      render(
        <Wrapper>
          <FormFieldRow
            field={{
              field: "priority",
              type: "radio",
              label: "Priority",
              options: [
                { value: "low", label: "Low" },
                { value: "high", label: "High" },
              ],
            }}
            resource="contacts.contact"
            record={{ priority: "low" }}
            onChange={vi.fn()}
            formReadonly={false}
          />
        </Wrapper>,
      );
      expect(screen.getByText("Priority").closest("label")?.querySelector("label")).toBeNull();
    });
  });
});
