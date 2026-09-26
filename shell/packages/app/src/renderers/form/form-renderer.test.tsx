import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetReportedConditionErrors } from "../../conditions/use-condition-evaluator.js";
import type { FormRendererProps } from "./form-renderer.js";
import { FormRenderer } from "./form-renderer.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import type { FormRecordHandle } from "./use-form-record.js";

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

const { useFormRecordMock, resolveModelMock } = vi.hoisted(() => ({
  useFormRecordMock: vi.fn(),
  resolveModelMock: vi.fn(async () => ({ shareable: false })),
}));
vi.mock("./use-form-record.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./use-form-record.js")>();
  return { ...actual, useFormRecord: useFormRecordMock };
});
vi.mock("./form-chatter.js", () => ({
  FormChatter: ({ recordId }: { recordId: string | undefined }) => (
    <section data-testid="form-chatter">{recordId ?? "unsaved"}</section>
  ),
}));
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

function renderForm(v: FormViewDeclaration = view, props: Partial<FormRendererProps> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <PermissionContext.Provider value={permissionValue}>
        <FormRenderer view={v} module="contacts" recordId="01j" {...props} />
      </PermissionContext.Provider>
    </QueryClientProvider>,
  );
}

describe("FormRenderer", () => {
  it("shows a loading state", () => {
    useFormRecordMock.mockReturnValue(handle({ isLoading: true }));
    const { container } = renderForm();
    expect(container.querySelector('[data-skeleton="lines"]')).toBeTruthy();
  });

  it("shows an error state with a retry that calls refetch", () => {
    const refetch = vi.fn();
    useFormRecordMock.mockReturnValue(handle({ isError: true, error: new Error("boom"), refetch }));
    renderForm();
    expect(screen.getByText("boom")).toBeTruthy();
    fireEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalled();
  });

  it("renders the view's label as the page title via PageHeader", () => {
    useFormRecordMock.mockReturnValue(handle());
    renderForm();
    expect(screen.getByRole("heading", { name: "Contact" })).toBeTruthy();
  });

  it("non-autosave: shows a Save button, disabled until dirty, that calls save() on click", () => {
    const save = vi.fn();
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

  // Test-only seam (form-renderer.stories.tsx's manual-save-saving/-error
  // and autosave-saving stories) — verifies it actually reaches
  // useFormRecord rather than assuming the prop threading is correct.
  it("threads testFormRecordOptions through to useFormRecord", () => {
    useFormRecordMock.mockReturnValue(handle());
    const registry = { resolve: vi.fn() };
    const client = { get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn() };
    renderForm(view, { testFormRecordOptions: { registry, client, autoSaveDelay: 10 } });
    expect(useFormRecordMock).toHaveBeenCalledWith(
      "contacts.contact",
      "01j",
      expect.objectContaining({ registry, client, autoSaveDelay: 10 }),
    );
  });
});

describe("FormRenderer conditions", () => {
  const fieldPermissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: { "contacts.contact": { email: { read: true, write: true }, phone: { read: true, write: true } } },
    modulesEnabled: new Set(),
  });
  const conditionalView: FormViewDeclaration = {
    ...view,
    sections: [
      {
        type: "fields",
        fields: [
          { field: "email", type: "email" },
          { field: "phone", type: "text", readonly_condition: "record.state = 'locked'" },
        ],
      },
    ],
  };

  afterEach(() => {
    vi.restoreAllMocks();
    resetReportedConditionErrors();
  });

  function renderConditional(v: FormViewDeclaration, record: Record<string, unknown>, isDirty = true) {
    useFormRecordMock.mockReturnValue(handle({ record, isDirty }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={fieldPermissions}>
          <FormRenderer view={v} module="contacts" recordId="01j" />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );
  }

  const record = { email: "a@b.com", phone: "555", state: "open" };

  it("leaves every field editable and Save enabled when the form's readonly_condition is false", () => {
    renderConditional({ ...conditionalView, readonly_condition: "record.state = 'done'" }, record);
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(false);
    expect((screen.getByDisplayValue("555") as HTMLInputElement).disabled).toBe(false);
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(false);
  });

  it("makes every field read-only and disables Save when the form's readonly_condition holds", () => {
    renderConditional({ ...conditionalView, readonly_condition: "record.state = 'open'" }, record);
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByDisplayValue("555") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(true);
  });

  it("locks only the field whose own readonly_condition holds, leaving Save enabled", () => {
    renderConditional(conditionalView, { ...record, state: "locked" });
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(false);
    expect((screen.getByDisplayValue("555") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(false);
  });

  it("re-evaluates the form's readonly_condition when the record changes", () => {
    const readonlyView = { ...conditionalView, readonly_condition: "record.state = 'done'" };
    const { rerender } = renderConditional(readonlyView, record);
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(false);

    useFormRecordMock.mockReturnValue(handle({ record: { ...record, state: "done" }, isDirty: true }));
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <PermissionContext.Provider value={fieldPermissions}>
          <FormRenderer view={readonlyView} module="contacts" recordId="01j" />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(true);
  });

  it("locks the form and disables Save, reporting the view, when readonly_condition is malformed", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    renderConditional({ ...conditionalView, readonly_condition: "record.state ==" }, record);
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(true);
    expect(String(consoleError.mock.calls[0]?.[0])).toContain('form "contacts_form"');
  });

  it("evaluates header_actions conditions against the form's record", () => {
    const actionView: FormViewDeclaration = {
      ...conditionalView,
      header_actions: [
        { label: "Reopen", type: "url", url: "https://a.example", condition: "record.state = 'done'" },
        { label: "Archive", type: "url", url: "https://b.example", condition: "record.state = 'open'" },
      ],
    };
    renderConditional(actionView, record);
    expect(screen.getByText("Archive")).toBeTruthy();
    expect(screen.queryByText("Reopen")).toBeNull();
  });
});

describe("FormRenderer chatter", () => {
  it("renders FormChatter for the record by default", () => {
    useFormRecordMock.mockReturnValue(handle());
    renderForm();
    expect(screen.getByTestId("form-chatter").textContent).toBe("01j");
  });

  it('omits the chatter when the view sets "chatter": false', () => {
    useFormRecordMock.mockReturnValue(handle());
    renderForm({ ...view, chatter: false });
    expect(screen.queryByTestId("form-chatter")).toBeNull();
  });

  it("invalidates the record's activity feed when a save succeeds", () => {
    useFormRecordMock.mockReturnValue(handle());
    const client = new QueryClient();
    const invalidateSpy = vi.spyOn(client, "invalidateQueries");
    render(
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={permissionValue}>
          <FormRenderer view={view} module="contacts" recordId="01j" />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );

    const options = useFormRecordMock.mock.calls[0]?.[2] as { onSaved?: (record: Record<string, unknown>) => void };
    options.onSaved?.({ id: "01j" });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["record-activity", "contacts.contact", "01j"] });
  });
});
