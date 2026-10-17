import { AuthContext, type AuthContextValue, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { wsManager } from "@goerp/sdk/realtime";
import type { ViewExtensionEntry } from "@goerp/sdk/schema";
import { simulateRealtimeMessage } from "@goerp/sdk/testing";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

const {
  useFormRecordMock,
  resolveModelMock,
  resolveRecordMock,
  resolveResourceMock,
  navigateMock,
  searchState,
  forTargetMock,
} = vi.hoisted(() => ({
  useFormRecordMock: vi.fn(),
  forTargetMock: vi.fn<() => Promise<ViewExtensionEntry[]>>(async () => []),
  resolveModelMock: vi.fn(async () => ({ shareable: false })),
  resolveRecordMock: vi.fn(async () => "/contacts/{id}" as string | null),
  resolveResourceMock: vi.fn(async () => ({ updatePermissions: [] as string[] | null })),
  navigateMock: vi.fn(),
  searchState: { value: {} as Record<string, unknown> },
}));
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useSearch: () => searchState.value,
    useBlocker: () => ({ status: "idle" }),
  };
});
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
  return {
    ...actual,
    modelRegistry: { resolve: resolveModelMock },
    viewExtensionRegistry: { forTarget: forTargetMock },
    viewPathRegistry: { resolveRecord: resolveRecordMock },
    resourceRegistry: { resolve: resolveResourceMock },
  };
});

beforeEach(() => {
  searchState.value = { edit: true };
  forTargetMock.mockReset().mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  useFormRecordMock.mockReset();
  resolveModelMock.mockClear();
  resolveRecordMock.mockClear();
  navigateMock.mockReset();
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
    isPreviewing: false,
    isUnavailable: false,
    unsavedEdits: {},
    setField: vi.fn(),
    reset: vi.fn(),
    save: vi.fn(async () => {}),
    isSaving: false,
    saveError: null,
    hasConflict: false,
    keepLocalEdits: vi.fn(),
    resolveConflict: vi.fn(),
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

  it("shows every field as a value and offers no Save when the form's readonly_condition holds", () => {
    renderConditional({ ...conditionalView, readonly_condition: "record.state = 'open'" }, record);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByRole("link", { name: "a@b.com" })).toBeTruthy();
    expect(screen.queryByText("Save")).toBeNull();
  });

  it("locks only the field whose own readonly_condition holds, leaving Save enabled", () => {
    renderConditional(conditionalView, { ...record, state: "locked" });
    expect((screen.getByDisplayValue("a@b.com") as HTMLInputElement).disabled).toBe(false);
    expect(screen.queryByDisplayValue("555")).toBeNull();
    expect(screen.getByText("555")).toBeTruthy();
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
    expect(screen.queryByDisplayValue("a@b.com")).toBeNull();
    expect(screen.queryByText("Save")).toBeNull();
  });

  it("locks the form and offers no Save, reporting the view, when readonly_condition is malformed", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    renderConditional({ ...conditionalView, readonly_condition: "record.state ==" }, record);
    expect(screen.queryByDisplayValue("a@b.com")).toBeNull();
    expect(screen.queryByText("Save")).toBeNull();
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
    // Saving an existing record only leaves edit mode: it does not move to another path.
    expect(navigateMock).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith({ search: expect.any(Function), replace: true });
  });

  function renderCreateForm(client = new QueryClient()) {
    useFormRecordMock.mockReturnValue(handle());
    const result = render(
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={permissionValue}>
          <FormRenderer view={view} module="contacts" />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );
    const options = useFormRecordMock.mock.calls[0]?.[2] as { onSaved?: (record: Record<string, unknown>) => void };
    return { ...result, onSaved: options.onSaved };
  }

  it("moves a create form to the new record's URL once the record is saved", async () => {
    const client = new QueryClient();
    const removeSpy = vi.spyOn(client, "removeQueries");
    const { onSaved } = renderCreateForm(client);
    onSaved?.({ id: "01new" });

    await vi.waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: "/_m/contacts/01new", replace: true }));
    expect(resolveRecordMock).toHaveBeenCalledWith("contacts_form", "contacts");
    // The next "New" form must not open prefilled with the record just created.
    await vi.waitFor(() => expect(removeSpy).toHaveBeenCalledWith({ queryKey: ["record", "contacts.contact", null] }));
  });

  it("in a modal, hands the created record to onCreated instead of navigating", async () => {
    const onCreated = vi.fn();
    const client = new QueryClient();
    const removeSpy = vi.spyOn(client, "removeQueries");
    useFormRecordMock.mockReturnValue(handle());
    render(
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={permissionValue}>
          <FormRenderer view={view} module="contacts" modal={{ onCreated }} />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );
    const options = useFormRecordMock.mock.calls[0]?.[2] as { onSaved?: (record: Record<string, unknown>) => void };
    options.onSaved?.({ id: "01new" });

    expect(onCreated).toHaveBeenCalledWith("01new");
    expect(removeSpy).toHaveBeenCalledWith({ queryKey: ["record", "contacts.contact", null] });
    await Promise.resolve();
    expect(resolveRecordMock).not.toHaveBeenCalled();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("stays put when the form unmounts before the record's path resolves", async () => {
    const { onSaved, unmount } = renderCreateForm();
    onSaved?.({ id: "01new" });
    unmount();

    await vi.waitFor(() => expect(resolveRecordMock).toHaveBeenCalled());
    await Promise.resolve();
    expect(navigateMock).not.toHaveBeenCalled();
  });
});

describe("FormRenderer UI push channel", () => {
  it("runs pushes on the shown record's ui:record channel until it unmounts", async () => {
    const info = vi.spyOn(toast, "info");
    useFormRecordMock.mockReturnValue(handle());
    const { unmount } = renderForm();
    const push = (message: string) =>
      simulateRealtimeMessage("ui:record:contacts.contact:01j", {
        type: "custom",
        event: "ui.push",
        payload: { instructions: [{ type: "toast", message, severity: "info" }] },
      });

    push("Saved by Ama");
    await vi.waitFor(() => expect(info).toHaveBeenCalledWith("Saved by Ama", { duration: 4000 }));
    unmount();
    push("after unmount");
    await Promise.resolve();

    expect(info).toHaveBeenCalledOnce();
    info.mockRestore();
    toast.dismiss();
  });
});

describe("FormRenderer display and edit modes", () => {
  function firstNavigation() {
    return navigateMock.mock.calls[0]?.at(0) as {
      search: (prev: Record<string, unknown>) => Record<string, unknown>;
      replace?: boolean;
    };
  }

  const contactView: FormViewDeclaration = {
    ...view,
    sections: [{ type: "fields", fields: [{ field: "email", label: "Email", type: "email" }] }],
  };
  const record = { email: "ada@acme.test" };

  function renderMode(
    v: FormViewDeclaration,
    handleOverrides: Partial<FormRecordHandle> = {},
    props: Partial<FormRendererProps> = {},
  ) {
    useFormRecordMock.mockReturnValue(handle({ record, ...handleOverrides }));
    return renderForm(v, props);
  }

  it("opens an existing record in display mode: values and an Edit button, no inputs or footer", async () => {
    searchState.value = {};
    renderMode(contactView);

    expect(screen.getByRole("link", { name: "ada@acme.test" }).getAttribute("href")).toBe("mailto:ada@acme.test");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByText("Save")).toBeNull();
    expect(screen.queryByText("Cancel")).toBeNull();
    expect(await screen.findByRole("button", { name: "Edit" })).toBeTruthy();
  });

  it("enters edit mode by adding the edit parameter to the path", async () => {
    searchState.value = {};
    renderMode(contactView);
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));

    expect(navigateMock).toHaveBeenCalledTimes(1);
    const { search, replace } = firstNavigation();
    expect(search({ tab: "billing" })).toEqual({ tab: "billing", edit: true });
    expect(replace).toBeUndefined();
  });

  it("shows inputs, Save and Cancel, and no Edit button, in edit mode", async () => {
    renderMode(contactView);

    expect((screen.getByDisplayValue("ada@acme.test") as HTMLInputElement).disabled).toBe(false);
    expect(screen.getByText("Save")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("cancels a clean edit straight back to display mode, dropping the edit parameter", () => {
    const reset = vi.fn();
    renderMode(contactView, { reset });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(reset).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    const { search, replace } = firstNavigation();
    expect(search({ tab: "billing", edit: true })).toEqual({ tab: "billing" });
    expect(replace).toBe(true);
  });

  it("asks before discarding a dirty edit, and keeps editing if the user declines", () => {
    const reset = vi.fn();
    renderMode(contactView, { reset, isDirty: true });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByText("Discard changes?")).toBeTruthy();
    expect(reset).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(reset).not.toHaveBeenCalled();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("discards a dirty edit once the user confirms", () => {
    const reset = vi.fn();
    renderMode(contactView, { reset, isDirty: true });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Discard" }));

    expect(reset).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledTimes(1);
  });

  it("cancels an edit with Escape", () => {
    const reset = vi.fn();
    renderMode(contactView, { reset });
    fireEvent.keyDown(screen.getByDisplayValue("ada@acme.test"), { key: "Escape" });

    expect(reset).toHaveBeenCalledTimes(1);
  });

  it("returns to display mode when a save of an existing record succeeds", () => {
    renderMode(contactView);
    const options = useFormRecordMock.mock.calls[0]?.[2] as { onSaved?: (record: Record<string, unknown>) => void };
    options.onSaved?.({ id: "01j" });

    expect(navigateMock).toHaveBeenCalledWith({ search: expect.any(Function), replace: true });
  });

  it("in a modal, switches between display and edit mode in place, leaving the URL alone", async () => {
    searchState.value = { edit: true };
    renderMode(contactView, {}, { modal: { onCreated: vi.fn() } });
    expect(screen.queryByRole("textbox")).toBeNull();

    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    expect((screen.getByDisplayValue("ada@acme.test") as HTMLInputElement).disabled).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await screen.findByRole("button", { name: "Edit" });
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("opens a new record in edit mode, with no Cancel, whatever the path says", () => {
    searchState.value = {};
    useFormRecordMock.mockReturnValue(handle());
    render(
      <QueryClientProvider client={new QueryClient()}>
        <PermissionContext.Provider value={permissionValue}>
          <FormRenderer view={contactView} module="contacts" />
        </PermissionContext.Provider>
      </QueryClientProvider>,
    );

    expect(screen.getByText("Save")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("keeps an autosave form in edit mode, with no Edit button", async () => {
    searchState.value = {};
    renderMode({ ...contactView, autosave: true });

    expect(screen.getByDisplayValue("ada@acme.test")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("keeps a read-only form in display mode with no Edit button, even with the edit parameter", async () => {
    renderMode({ ...contactView, readonly_condition: "true = true" });
    await waitFor(() => expect(resolveResourceMock).toHaveBeenCalled());

    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByText("Save")).toBeNull();
  });

  it("shows no Edit button to a user who lacks the update permission", async () => {
    searchState.value = {};
    resolveResourceMock.mockResolvedValueOnce({ updatePermissions: ["contacts:contact:write"] });
    renderMode(contactView);
    await waitFor(() => expect(resolveResourceMock).toHaveBeenCalled());

    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("announces the change of mode to assistive technology", () => {
    renderMode(contactView);
    expect(screen.getAllByRole("status").some((el) => el.classList.contains("sr-only"))).toBe(true);
  });
});

describe("unavailable form", () => {
  it("removes the form and server values, disables saving and keeps recoverable edits", async () => {
    useFormRecordMock.mockReturnValue(
      handle({
        isUnavailable: true,
        isDirty: true,
        record: { name: "Old server value" },
        unsavedEdits: { name: "Local draft" },
      }),
    );
    renderForm();
    const heading = await screen.findByRole("heading", { name: "This record is no longer available" });
    expect(heading).toBe(document.activeElement);
    expect(screen.queryByText("Old server value")).toBeNull();
    expect(screen.queryByTestId("form-chatter")).toBeNull();
    const save = screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    const edits = screen.getByRole("textbox", { name: "Unsaved changes" }) as HTMLTextAreaElement;
    expect(edits.value).toBe(JSON.stringify({ name: "Local draft" }, null, 2));
    expect(edits.readOnly).toBe(true);
  });
});

describe("FormRenderer collaboration", () => {
  const emailView: FormViewDeclaration = {
    ...view,
    sections: [{ type: "fields", fields: [{ field: "email", label: "Email", type: "email" }] }],
  };
  const presenceEntry = (userId: string, name: string) => ({
    user_id: userId,
    name,
    avatar_url: null,
    editing_field: null,
    joined_at: "2026-10-14T09:00:00Z",
    last_seen_at: "2026-10-14T09:00:30Z",
  });

  function renderAs(userId: string, v: FormViewDeclaration = view) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={client}>
        <AuthContext.Provider value={{ user: { id: userId } } as AuthContextValue}>
          <PermissionContext.Provider value={permissionValue}>
            <FormRenderer view={v} module="contacts" recordId="01j" />
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>,
    );
  }

  it("shows the other users present as avatars, in display mode too", () => {
    searchState.value = {};
    useFormRecordMock.mockReturnValue(handle());
    renderAs("me");
    act(() =>
      simulateRealtimeMessage("record:contacts.contact:01j", {
        type: "custom",
        event: "presence.update",
        payload: {
          resource: "contacts.contact",
          record_id: "01j",
          users: [presenceEntry("me", "Me Myself"), presenceEntry("ama", "Ama Mensah")],
        },
      }),
    );

    const list = screen.getByRole("list", { name: "Also on this record" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(list).getByRole("img", { name: "Ama Mensah" })).toBeTruthy();

    act(() =>
      simulateRealtimeMessage("record:contacts.contact:01j", {
        type: "custom",
        event: "presence.update",
        payload: { resource: "contacts.contact", record_id: "01j", users: [presenceEntry("me", "Me Myself")] },
      }),
    );
    expect(screen.queryByRole("list", { name: "Also on this record" })).toBeNull();
  });

  it("reports the focused field while editing and clears it on blur", () => {
    const setFocus = vi.spyOn(wsManager, "setPresenceFocus");
    try {
      useFormRecordMock.mockReturnValue(handle({ record: { email: "a@acme.test" } }));
      renderAs("me", emailView);
      const input = screen.getByRole("textbox", { name: "Email" });

      act(() => input.focus());
      expect(setFocus).toHaveBeenLastCalledWith("record:contacts.contact:01j", "email");
      act(() => input.blur());
      expect(setFocus).toHaveBeenLastCalledWith("record:contacts.contact:01j", null);
    } finally {
      setFocus.mockRestore();
    }
  });

  it("offers to keep the local edits or load the saved version on a conflict", () => {
    const keepLocalEdits = vi.fn();
    const resolveConflict = vi.fn();
    useFormRecordMock.mockReturnValue(handle({ hasConflict: true, isDirty: true, keepLocalEdits, resolveConflict }));
    renderAs("me");

    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("Someone else saved changes to fields you're editing.");
    fireEvent.click(within(alert).getByRole("button", { name: "Keep my changes" }));
    expect(keepLocalEdits).toHaveBeenCalledOnce();
    fireEvent.click(within(alert).getByRole("button", { name: "Load saved version" }));
    expect(resolveConflict).toHaveBeenCalledOnce();
  });

  it("never shows the conflict warning in display mode", () => {
    searchState.value = {};
    useFormRecordMock.mockReturnValue(handle({ hasConflict: true }));
    renderAs("me");
    expect(screen.queryByText("Someone else saved changes to fields you're editing.")).toBeNull();
  });
});

describe("FormRenderer fields extensions", () => {
  function extension(target: string, position: "append" | "prepend", field: string): ViewExtensionEntry {
    return {
      module: "l10n",
      moduleDisplayName: "Localisation",
      loadOrder: 1,
      ref: { extends: "contacts.contacts_form", extension: field },
      definition: {
        name: field,
        type: "fields",
        target_section: target,
        position,
        fields: [{ field, label: field }],
      },
    };
  }

  it("renders extension fields in dependency order and writes through the form", async () => {
    const setField = vi.fn();
    useFormRecordMock.mockReturnValue(handle({ record: { street: "High Street" }, setField }));
    forTargetMock.mockResolvedValue([
      extension("address", "prepend", "first"),
      extension("address", "prepend", "second"),
      extension("address", "append", "region"),
    ]);
    renderForm({ ...view, sections: [{ name: "address", fields: [{ field: "street", label: "Street" }] }] });

    await screen.findByLabelText("region");
    expect(screen.getAllByRole("textbox").map((input) => input.getAttribute("id"))).toEqual([
      screen.getByLabelText("first").id,
      screen.getByLabelText("second").id,
      screen.getByLabelText("Street").id,
      screen.getByLabelText("region").id,
    ]);
    fireEvent.change(screen.getByLabelText("region"), { target: { value: "Greater Accra" } });
    expect(setField).toHaveBeenCalledWith({ region: "Greater Accra" });
    expect(forTargetMock).toHaveBeenCalledWith("contacts", "contacts_form");
  });

  it("applies fields to named tab sections and preserves field permission checks", async () => {
    useFormRecordMock.mockReturnValue(handle());
    const restricted = extension("address", "append", "restricted");
    restricted.definition = {
      ...restricted.definition!,
      fields: [{ field: "restricted", permission: "l10n:secret:read" }],
    };
    forTargetMock.mockResolvedValue([extension("address", "append", "region"), restricted]);
    renderForm({
      ...view,
      tabs: [{ label: "Address", type: "fields", sections: [{ name: "address", fields: [] }] }],
    });

    expect(await screen.findByLabelText("region")).toBeTruthy();
    expect(screen.queryByLabelText("restricted")).toBeNull();
  });

  it("skips missing sections and malformed field definitions", async () => {
    useFormRecordMock.mockReturnValue(handle());
    const malformed = extension("address", "append", "invalid");
    malformed.definition = { ...malformed.definition!, fields: [{ type: "text" }] };
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    forTargetMock.mockResolvedValue([extension("missing", "append", "region"), malformed]);
    renderForm({ ...view, sections: [{ name: "address", fields: [{ field: "street", label: "Street" }] }] });

    await waitFor(() => expect(warn).toHaveBeenCalledWith(expect.stringContaining("invalid fields")));
    expect(screen.queryByLabelText("region")).toBeNull();
    expect(screen.getAllByRole("textbox")).toHaveLength(1);
    warn.mockRestore();
  });
});
