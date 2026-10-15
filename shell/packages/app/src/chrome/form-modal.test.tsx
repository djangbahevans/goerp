import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { modalManager } from "@goerp/sdk/realtime";
import { buildViewRegistry, type MetaSchema, type ModelDef, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

function mount(permissions = ["contacts:contact:read", "contacts:contact:create"]) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ViewRegistryContext value={registry}>
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
      </ViewRegistryContext>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
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
});
