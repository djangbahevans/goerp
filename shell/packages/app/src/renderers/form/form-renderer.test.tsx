import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FormRenderer } from "./form-renderer.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import type { FormRecordHandle } from "./use-form-record.js";

const { useFormRecordMock, resolveModelMock } = vi.hoisted(() => ({
  useFormRecordMock: vi.fn(),
  resolveModelMock: vi.fn(async () => ({ shareable: false })),
}));
vi.mock("./use-form-record.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./use-form-record.js")>();
  return { ...actual, useFormRecord: useFormRecordMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});

afterEach(() => {
  cleanup();
  useFormRecordMock.mockReset();
  resolveModelMock.mockClear();
});

const view: FormViewDeclaration = {
  name: "contacts_form",
  type: "form",
  resource: "contacts.contact",
  label: "Contact",
  sections: [],
};

function handle(overrides: Partial<FormRecordHandle> = {}): FormRecordHandle {
  return {
    record: {},
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    isDirty: false,
    setField: vi.fn(),
    save: vi.fn(async () => {}),
    isSaving: false,
    saveError: null,
    ...overrides,
  };
}

function renderForm(v: FormViewDeclaration = view) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <FormRenderer view={v} module="contacts" recordId="01j" />
    </QueryClientProvider>,
  );
}

describe("FormRenderer", () => {
  it("shows a loading state", () => {
    useFormRecordMock.mockReturnValue(handle({ isLoading: true }));
    renderForm();
    expect(screen.getByRole("status", { name: /Loading Contact/ })).toBeTruthy();
  });

  it("shows an error state with a retry that calls refetch", () => {
    const refetch = vi.fn();
    useFormRecordMock.mockReturnValue(handle({ isError: true, error: new Error("boom"), refetch }));
    renderForm();
    expect(screen.getByText("boom")).toBeTruthy();
    fireEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalled();
  });

  it("non-autosave: shows a Save button, disabled until dirty, that calls save() on click", () => {
    const save = vi.fn(async () => {});
    useFormRecordMock.mockReturnValue(handle({ isDirty: true, save }));
    renderForm();
    const button = screen.getByText("Save");
    expect((button as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(button);
    expect(save).toHaveBeenCalled();
  });

  it("non-autosave: Save is disabled when there are no local edits", () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: false }));
    renderForm();
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(true);
  });

  it("autosave: renders no Save button, and shows a saving indicator while a save is in flight", () => {
    useFormRecordMock.mockReturnValue(handle({ isSaving: true }));
    renderForm({ ...view, autosave: true });
    expect(screen.queryByText("Save")).toBeNull();
    expect(screen.getByText("Saving…")).toBeTruthy();
  });

  it("surfaces a save error", () => {
    useFormRecordMock.mockReturnValue(handle({ saveError: new Error("conflict") }));
    renderForm();
    expect(screen.getByText("conflict")).toBeTruthy();
  });
});
