import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createBrowserHistory,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useNavigate,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FormRenderer } from "./form-renderer.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import type { FormRecordHandle } from "./use-form-record.js";

const { useFormRecordMock, resolveRecordMock } = vi.hoisted(() => ({
  useFormRecordMock: vi.fn(),
  resolveRecordMock: vi.fn(async () => "/contacts/{id}" as string | null),
}));
vi.mock("./use-form-record.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./use-form-record.js")>();
  return { ...actual, useFormRecord: useFormRecordMock };
});
vi.mock("./form-chatter.js", () => ({ FormChatter: () => null }));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return {
    ...actual,
    modelRegistry: { resolve: vi.fn(async () => ({ shareable: false })) },
    viewPathRegistry: { resolveRecord: resolveRecordMock },
    resourceRegistry: { resolve: vi.fn(async () => ({ updatePermissions: [] as string[] | null })) },
  };
});

afterEach(() => {
  cleanup();
  useFormRecordMock.mockReset();
});

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
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
    reset: vi.fn(),
    save: vi.fn(),
    isSaving: false,
    saveError: null,
    ...overrides,
  };
}

// Only the browser history listens for beforeunload; the memory history has no tab to close.
function browserHistoryAt(...paths: string[]) {
  const [first = "/", ...rest] = paths;
  window.history.replaceState({}, "", first);
  for (const path of rest) window.history.pushState({}, "", path);
  return createBrowserHistory();
}

// Buttons rather than <Link>s: the routes here are built ad hoc, outside the app's typed route tree.
function NavLinks() {
  const navigate = useNavigate() as unknown as (opts: { to: string; search?: Record<string, unknown> }) => void;
  return (
    <>
      <button type="button" onClick={() => navigate({ to: "/other" })}>
        Other page
      </button>
      <button type="button" onClick={() => navigate({ to: "/contacts/01j", search: { edit: true, tab: "notes" } })}>
        Notes tab
      </button>
      <button type="button" onClick={() => navigate({ to: "/contacts/01j", search: { tab: "notes" } })}>
        Display notes
      </button>
    </>
  );
}

// A form route and a sibling page, with links between them, on a real router.
async function renderFormRoute(
  initialPath: string,
  options: {
    view?: FormViewDeclaration;
    recordId?: string | undefined;
    browserHistory?: boolean;
    entries?: string[];
  } = {},
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={permissionValue}>
          <Outlet />
        </PermissionContext.Provider>
      </QueryClientProvider>
    ),
  });
  const formRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/contacts/$id",
    component: () => (
      <>
        <NavLinks />
        <FormRenderer
          view={options.view ?? view}
          module="contacts"
          {...("recordId" in options ? {} : { recordId: "01j" })}
        />
      </>
    ),
  });
  const otherRoute = createRoute({ getParentRoute: () => rootRoute, path: "/other", component: () => <p>Other</p> });
  const router = createRouter({
    routeTree: rootRoute.addChildren([formRoute, otherRoute]),
    history: options.browserHistory
      ? browserHistoryAt(...(options.entries ?? [initialPath]))
      : createMemoryHistory({ initialEntries: [initialPath] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return { router };
}

function beforeUnloadPrevented(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

describe("FormRenderer unsaved-changes guard", () => {
  it("asks before in-app navigation away from a dirty edit, and Stay keeps the form", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    const { router } = await renderFormRoute("/contacts/01j?edit=true");

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));

    expect(await screen.findByText("Leave without saving?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Stay" }));
    await waitFor(() => expect(screen.queryByText("Leave without saving?")).toBeNull());
    expect(router.state.location.pathname).toBe("/contacts/01j");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
  });

  it("Leave navigates away", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    const { router } = await renderFormRoute("/contacts/01j?edit=true");

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));
    fireEvent.click(await screen.findByRole("button", { name: "Leave" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/other"));
  });

  it("Escape does not cancel the edit while the leave dialog is open", async () => {
    const reset = vi.fn();
    useFormRecordMock.mockReturnValue(handle({ isDirty: true, reset }));
    await renderFormRoute("/contacts/01j?edit=true");

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));
    await screen.findByText("Leave without saving?");
    fireEvent.keyDown(document, { key: "Escape" });

    expect(reset).not.toHaveBeenCalled();
    expect(screen.queryByText("Discard changes?")).toBeNull();
  });

  it("does not ask when the edit has no unsaved changes", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: false }));
    const { router } = await renderFormRoute("/contacts/01j?edit=true");

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/other"));
    expect(screen.queryByText("Leave without saving?")).toBeNull();
  });

  it("never asks in display mode", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    const { router } = await renderFormRoute("/contacts/01j");

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/other"));
    expect(screen.queryByText("Leave without saving?")).toBeNull();
    expect(beforeUnloadPrevented()).toBe(false);
  });

  it("never asks for an autosave form", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    const { router } = await renderFormRoute("/contacts/01j", { view: { ...view, autosave: true } });

    fireEvent.click(screen.getByRole("button", { name: "Other page" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/other"));
    expect(beforeUnloadPrevented()).toBe(false);
  });

  it("does not hold a change to the form's own search parameters", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    const { router } = await renderFormRoute("/contacts/01j?edit=true");

    fireEvent.click(screen.getByRole("button", { name: "Notes tab" }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ tab: "notes" }));
    expect(screen.queryByText("Leave without saving?")).toBeNull();
  });

  describe("leaving edit mode on the same path", () => {
    const entries = ["/contacts/01j", "/contacts/01j?edit=true"];

    it("asks on Back from a dirty edit, and Stay keeps the edit entry and its form", async () => {
      useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
      const { router } = await renderFormRoute("/contacts/01j?edit=true", { entries, browserHistory: true });

      act(() => window.history.back());

      expect(await screen.findByText("Leave without saving?")).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "Stay" }));
      await waitFor(() => expect(screen.queryByText("Leave without saving?")).toBeNull());
      expect(router.state.location.search).toMatchObject({ edit: true });
      expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    });

    it("Leave on Back returns to display mode and drops the edits", async () => {
      const reset = vi.fn();
      useFormRecordMock.mockReturnValue(handle({ isDirty: true, reset }));
      const { router } = await renderFormRoute("/contacts/01j?edit=true", { entries, browserHistory: true });

      act(() => window.history.back());
      fireEvent.click(await screen.findByRole("button", { name: "Leave" }));

      await waitFor(() => expect(router.state.location.search).not.toHaveProperty("edit"));
      expect(reset).toHaveBeenCalled();
    });

    it("asks on Forward out of a dirty edit entry", async () => {
      useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
      const { router } = await renderFormRoute("/contacts/01j?edit=true", {
        entries: ["/contacts/01j?edit=true", "/contacts/01j"],
        browserHistory: true,
      });
      act(() => window.history.back());
      await waitFor(() => expect(router.state.location.search).toMatchObject({ edit: true }));

      act(() => window.history.forward());

      expect(await screen.findByText("Leave without saving?")).toBeTruthy();
    });

    it("asks on an in-app link that drops the edit parameter", async () => {
      useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
      await renderFormRoute("/contacts/01j?edit=true");

      fireEvent.click(screen.getByRole("button", { name: "Display notes" }));

      expect(await screen.findByText("Leave without saving?")).toBeTruthy();
    });

    it("does not ask on Back when the edit has no unsaved changes", async () => {
      useFormRecordMock.mockReturnValue(handle({ isDirty: false }));
      const { router } = await renderFormRoute("/contacts/01j?edit=true", { entries, browserHistory: true });

      act(() => window.history.back());

      await waitFor(() => expect(router.state.location.search).not.toHaveProperty("edit"));
      expect(screen.queryByText("Leave without saving?")).toBeNull();
    });

    it("does not ask when a confirmed Cancel leaves edit mode", async () => {
      const reset = vi.fn();
      useFormRecordMock.mockReturnValue(handle({ isDirty: true, reset }));
      const { router } = await renderFormRoute("/contacts/01j?edit=true", { entries, browserHistory: true });

      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      fireEvent.click(await screen.findByRole("button", { name: "Discard" }));

      await waitFor(() => expect(router.state.location.search).not.toHaveProperty("edit"));
      expect(screen.queryByText("Leave without saving?")).toBeNull();
    });

    it("does not ask when Save leaves edit mode", async () => {
      let onSaved: ((record: Record<string, unknown>) => void) | undefined;
      useFormRecordMock.mockImplementation(
        (_resource: string, _id: string | undefined, options: { onSaved?: typeof onSaved }) => {
          onSaved = options.onSaved;
          return handle({ isDirty: true });
        },
      );
      const { router } = await renderFormRoute("/contacts/01j?edit=true", { entries, browserHistory: true });

      await act(async () => {
        onSaved?.({ id: "01j" });
      });

      await waitFor(() => expect(router.state.location.search).not.toHaveProperty("edit"));
      expect(screen.queryByText("Leave without saving?")).toBeNull();
    });
  });

  it("prompts on closing or reloading the tab only while the edit is dirty", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    await renderFormRoute("/contacts/01j?edit=true", { browserHistory: true });
    expect(beforeUnloadPrevented()).toBe(true);

    cleanup();
    useFormRecordMock.mockReturnValue(handle({ isDirty: false }));
    await renderFormRoute("/contacts/01j?edit=true", { browserHistory: true });
    expect(beforeUnloadPrevented()).toBe(false);
  });

  it("stops prompting once the form unmounts", async () => {
    useFormRecordMock.mockReturnValue(handle({ isDirty: true }));
    await renderFormRoute("/contacts/01j?edit=true", { browserHistory: true });
    cleanup();

    expect(beforeUnloadPrevented()).toBe(false);
  });

  it("moves a created record to its own path without asking", async () => {
    let onSaved: ((record: Record<string, unknown>) => void) | undefined;
    useFormRecordMock.mockImplementation(
      (_resource: string, _id: string | undefined, options: { onSaved?: typeof onSaved }) => {
        onSaved = options.onSaved;
        return handle({ isDirty: true });
      },
    );
    const { router } = await renderFormRoute("/contacts/new", { recordId: undefined });

    await act(async () => {
      onSaved?.({ id: "01new" });
    });

    await waitFor(() => expect(router.state.location.pathname).toBe("/_m/contacts/01new"));
    expect(screen.queryByText("Leave without saving?")).toBeNull();
  });
});
