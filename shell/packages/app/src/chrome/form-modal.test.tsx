import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { configureUIExecutor, executeInstructions, modalManager } from "@goerp/sdk/realtime";
import {
  buildViewRegistry,
  type MetaSchema,
  type ModelDef,
  type ViewRegistry,
  ViewRegistryContext,
  ViewRegistryStatusContext,
} from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { type ReactNode, useEffect, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FormRendererProps } from "../renderers/form/form-renderer.js";
import { useReportUnsavedChanges } from "../renderers/form/unsaved-changes.js";
import { resolveFormModalView } from "./form-modal.js";
import { ModalRoot } from "./modal-root.js";

const { router } = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  const router = {
    pathname: "/_m/contacts",
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    go(pathname: string) {
      router.pathname = pathname;
      for (const listener of listeners) listener();
    },
  };
  return { router };
});
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { useSyncExternalStore } = await import("react");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    useRouterState: ({ select }: { select: (s: { location: { pathname: string } }) => unknown }) =>
      select({ location: { pathname: useSyncExternalStore(router.subscribe, () => router.pathname) } }),
  };
});

vi.mock("../renderers/form/form-renderer.js", async () => {
  const { useState } = await import("react");
  return {
    FormRenderer: ({ view, recordId, modal }: FormRendererProps) => {
      const [dirty, setDirty] = useState(false);
      useReportUnsavedChanges(dirty);
      return (
        <section>
          <h1>{`${view.label} ${recordId ?? "new"}`}</h1>
          <button type="button" onClick={() => setDirty(true)}>
            Type
          </button>
          <button type="button" onClick={() => modal?.onCreated("01new")}>
            Save new
          </button>
        </section>
      );
    },
  };
});

function route(overrides: Partial<MetaSchema["modules"][string]["routes"][number]>) {
  return { method: "GET", path: "", permissions: [], response_is_list: false, ...overrides };
}

function model(name: string): ModelDef {
  return { name, label: name, label_plural: `${name}s`, fields: [], enabled_ops: [], shareable: false };
}

const schema: MetaSchema = {
  engine_version: "test",
  schema_hash: "form-modal",
  modules: {
    contacts: {
      name: "contacts",
      version: "1.0.0",
      display_name: "Contacts",
      navigation: [],
      models: {
        "contacts.contact": model("contact"),
        "contacts.note": model("note"),
      },
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      error_codes: [],
      notification_types: [],
      load_order: 0,
      public_config: {},
      views: [
        { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts", columns: [] },
        { name: "contacts_form", type: "form", resource: "contacts.contact", label: "Contact", sections: [] },
        { name: "notes_list", type: "list", resource: "contacts.note", label: "Notes", columns: [] },
      ],
      routes: [
        route({ path: "/contacts", model: "contacts.contact", crud_action: "list", engine_native: true }),
        route({ path: "/contacts/{id}", model: "contacts.contact", crud_action: "get", engine_native: true }),
        route({ path: "/notes", model: "contacts.note", crud_action: "list", engine_native: true }),
        route({ path: "/contacts", view: "contacts_list" }),
        route({ path: "/contacts/{id}", view: "contacts_form", permissions: ["contacts:contact:read"] }),
        route({
          method: "POST",
          path: "/contacts",
          view: "contacts_form",
          crud_action: "create",
          permissions: ["contacts:contact:create"],
        }),
      ],
    },
  },
};

const registry = buildViewRegistry(schema);

const registryState = {
  current: registry as ViewRegistry | null,
  status: "ready" as "loading" | "ready" | "error",
  listeners: new Set<() => void>(),
  set(next: Partial<{ current: ViewRegistry | null; status: "loading" | "ready" | "error" }>) {
    Object.assign(registryState, next);
    for (const listener of registryState.listeners) listener();
  },
};

function LiveRegistry({ children }: { children: ReactNode }) {
  const [, rerender] = useState(0);
  useEffect(() => {
    const listener = () => rerender((n) => n + 1);
    registryState.listeners.add(listener);
    return () => {
      registryState.listeners.delete(listener);
    };
  }, []);
  return (
    <ViewRegistryStatusContext value={registryState.status}>
      <ViewRegistryContext value={registryState.current}>{children}</ViewRegistryContext>
    </ViewRegistryStatusContext>
  );
}

function mount(permissions = ["contacts:contact:read", "contacts:contact:create"]) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <LiveRegistry>
        <PermissionContext
          value={createPermissionContextValue({
            permissions: new Set(permissions),
            fieldAccess: {},
            modulesEnabled: new Set(["contacts"]),
          })}
        >
          <button type="button">Open contact</button>
          <ModalRoot />
        </PermissionContext>
      </LiveRegistry>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  registryState.current = registry;
  registryState.status = "ready";
  configureUIExecutor(null);
  toast.dismiss();
  vi.restoreAllMocks();
  while (modalManager.getStack().length > 0) modalManager.closeTop();
  router.go("/_m/contacts");
});

describe("resolveFormModalView", () => {
  it("picks the resource's form through its record route, or its create route for a new record", () => {
    expect(resolveFormModalView(registry, "contacts.contact", "01j")).toMatchObject({
      viewName: "contacts_form",
      permissions: ["contacts:contact:read"],
    });
    expect(resolveFormModalView(registry, "contacts.contact", undefined)).toMatchObject({
      viewName: "contacts_form",
      permissions: ["contacts:contact:create"],
    });
  });

  it("returns null for a resource with no form view", () => {
    expect(resolveFormModalView(registry, "contacts.note", "01j")).toBeNull();
    expect(resolveFormModalView(registry, "contacts.unknown", undefined)).toBeNull();
  });
});

describe("FormModal", () => {
  it("opens an existing record's form, and a new-record form without record_id", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    expect(await screen.findByRole("heading", { name: "Contact 01j" })).toBeTruthy();
    expect(screen.getByRole("dialog", { name: "Contact" })).toBeTruthy();

    act(() => modalManager.closeTop());
    act(() => modalManager.open({ resource: "contacts.contact" }));
    expect(await screen.findByRole("heading", { name: "Contact new" })).toBeTruthy();
  });

  it("moves to the created record in place once a new record is saved", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save new" }));

    expect(await screen.findByRole("heading", { name: "Contact 01new" })).toBeTruthy();
    expect(modalManager.getStack()).toHaveLength(1);
  });

  it("shows an error inside the modal for a resource with no form view", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.note", recordId: "01j" }));
    expect((await screen.findByRole("alert")).textContent).toBe("There's no form to open this record.");
  });

  it("shows an error inside the modal when the user can't open the form", async () => {
    mount([]);
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    expect((await screen.findByRole("alert")).textContent).toBe("You don't have access to this record.");
    expect(screen.queryByRole("heading", { name: "Contact 01j" })).toBeNull();
  });

  it("closes from the close button straight away when nothing is unsaved", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));

    expect(modalManager.getStack()).toEqual([]);
  });

  it("asks before Escape discards unsaved changes, and stays open on Stay", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    fireEvent.click(await screen.findByRole("button", { name: "Type" }));

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("Leave without saving?")).toBeTruthy();
    fireEvent.click(within(confirm).getByRole("button", { name: "Stay" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(modalManager.getStack()).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Leave" }));
    expect(modalManager.getStack()).toEqual([]);
  });

  it("closes when the page underneath navigates", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    await screen.findByRole("heading", { name: "Contact 01j" });

    act(() => router.go("/_m/contacts/02k"));
    expect(modalManager.getStack()).toEqual([]);
  });

  it("stays open across a navigation a navigate instruction starts", async () => {
    configureUIExecutor({ queryClient: new QueryClient(), navigate: (href) => router.go(href) });
    mount();
    await act(() =>
      executeInstructions(
        [
          { type: "open_form", resource: "contacts.contact", record_id: "01j" },
          { type: "navigate", path: "/contacts/02k" },
        ],
        "response",
      ),
    );

    expect(router.pathname).toBe("/_m/contacts/02k");
    expect(await screen.findByRole("heading", { name: "Contact 01j" })).toBeTruthy();

    act(() => router.go("/_m/notes"));
    expect(modalManager.getStack()).toEqual([]);
  });

  it("still closes on a user navigation after a navigate instruction that kept the path", async () => {
    configureUIExecutor({ queryClient: new QueryClient(), navigate: (href) => router.go(href) });
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    await screen.findByRole("heading", { name: "Contact 01j" });
    await act(() => executeInstructions([{ type: "navigate", path: "/contacts" }], "response"));
    expect(modalManager.getStack()).toHaveLength(1);

    act(() => router.go("/_m/notes"));
    expect(modalManager.getStack()).toEqual([]);
  });

  it("closes with a toast when the created record has no form the user can open", async () => {
    const success = vi.spyOn(toast, "success");
    mount(["contacts:contact:create"]);
    act(() => modalManager.open({ resource: "contacts.contact" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save new" }));

    expect(modalManager.getStack()).toEqual([]);
    expect(success).toHaveBeenCalledWith("Saved.");
  });

  it("shows a loading state, not an error, until the registry is ready", async () => {
    registryState.status = "loading";
    registryState.current = null;
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(screen.queryByRole("alert")).toBeNull();

    act(() => registryState.set({ status: "ready", current: registry }));
    expect(await screen.findByRole("heading", { name: "Contact 01j" })).toBeTruthy();
  });

  it("holds an updated declaration while the form has unsaved changes", async () => {
    mount();
    act(() => modalManager.open({ resource: "contacts.contact", recordId: "01j" }));
    fireEvent.click(await screen.findByRole("button", { name: "Type" }));

    const renamed = structuredClone(schema);
    const form = renamed.modules.contacts?.views.find((view) => (view as { name: string }).name === "contacts_form");
    (form as { label: string }).label = "Customer";
    act(() => registryState.set({ current: buildViewRegistry(renamed) }));

    expect(screen.getByRole("heading", { name: "Contact 01j" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Customer 01j" })).toBeNull();
  });
});
