import { createPermissionContextValue, type FieldAccessMap, PermissionContext } from "@goerp/sdk/auth";
import {
  buildEmptyViewRegistry,
  type ModelDef,
  type NavigationGroup,
  type ResolvedView,
  type ViewExtensionEntry,
  type ViewRegistry,
  ViewRegistryContext,
  viewExtensionRegistry,
} from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { viewDebugRecorder } from "./view-debug.js";
import { ViewDebugOverlay } from "./view-debug-overlay.js";

afterEach(() => {
  cleanup();
  viewDebugRecorder.sync(false, "");
  vi.restoreAllMocks();
});

const CONTACT_LIST: ResolvedView = {
  module: "contacts",
  viewName: "contacts_list",
  viewType: "list",
  declaration: {
    name: "contacts_list",
    type: "list",
    resource: "contacts.contact",
    label: "Contacts",
    columns: [{ field: "name" }, { field: "iban" }],
  },
  permissions: [],
  bundleUrl: null,
};

const CONTACT_MODEL: ModelDef = {
  name: "contacts.contact",
  label: "Contact",
  label_plural: "Contacts",
  fields: [
    { name: "name", type: "text" },
    { name: "iban", type: "text" },
    { name: "salary", type: "decimal" },
  ],
  enabled_ops: [],
  shareable: false,
};

const NAVIGATION: NavigationGroup[] = [
  {
    key: "contacts:contacts",
    label: "Contacts",
    icon: "folder",
    module: "contacts",
    children: [
      { key: "contacts:contacts:all", label: "All contacts", path: "/_m/contacts", icon: "circle" },
      {
        key: "contacts:contacts:vip",
        label: "VIP",
        path: "/_m/contacts/vip",
        icon: "circle",
        permission: "contacts:vip:read",
      },
    ],
  },
  {
    key: "hr:people",
    label: "People",
    icon: "folder",
    module: "hr",
    children: [{ key: "hr:people:all", label: "Employees", path: "/_m/hr", icon: "circle" }],
  },
];

const FIELD_ACCESS: FieldAccessMap = {
  "contacts.contact": {
    iban: { read: false, write: false, denied_read: "mask" },
    salary: { read: false, write: false, denied_read: "omit" },
  },
};

async function renderOverlay(path: string, view: ResolvedView | null) {
  const registry: ViewRegistry = {
    ...buildEmptyViewRegistry(),
    models: new Map([[CONTACT_MODEL.name, CONTACT_MODEL]]),
    navigationTree: NAVIGATION,
    engineEnvironment: "development",
  };
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: FIELD_ACCESS,
    modulesEnabled: new Set(["contacts"]),
  });
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <ViewDebugOverlay />
      </>
    ),
  });
  const moduleRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/_m/$",
    loader: () => view,
    component: () => null,
  });
  const otherRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: () => null });
  const router = createRouter({
    routeTree: rootRoute.addChildren([moduleRoute, otherRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PermissionContext.Provider value={permissions}>
        <ViewRegistryContext.Provider value={registry}>
          <RouterProvider router={router} />
        </ViewRegistryContext.Provider>
      </PermissionContext.Provider>
    </QueryClientProvider>,
  );
  const overlay = await screen.findByRole("complementary", { name: "View debug" });
  return { router, overlay };
}

const section = (overlay: HTMLElement, title: string) => {
  const summary = within(overlay).getByText(title, { selector: "summary, summary *" });
  return summary.closest("details") as HTMLElement;
};

describe("ViewDebugOverlay", () => {
  it("lists the view's manifest elements and its declaration", async () => {
    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const declaration = section(overlay, "Declaration");

    expect(declaration.textContent).toContain("contacts.contacts_list");
    expect(declaration.textContent).toContain("declared in the contacts manifest");
    expect(within(declaration).getByText("views.contacts_list.columns[1]")).toBeTruthy();
    expect(within(declaration).getByText('field "iban"')).toBeTruthy();
  });

  it("lists applied extensions with their source module", async () => {
    const entries: ViewExtensionEntry[] = [
      {
        module: "crm",
        moduleDisplayName: "CRM",
        loadOrder: 2,
        ref: { extends: "contacts.contacts_list", extension: "lead_score" },
        definition: { name: "lead_score", type: "columns", position: "append" },
      },
      {
        module: "broken",
        moduleDisplayName: "Broken",
        loadOrder: 3,
        ref: { extends: "contacts.contacts_list", extension: "missing" },
        definition: undefined,
      },
    ];
    const forTarget = vi.spyOn(viewExtensionRegistry, "forTarget").mockResolvedValue(entries);
    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const extensions = section(overlay, "View extensions");

    await waitFor(() => expect(within(extensions).getByText("lead_score")).toBeTruthy());
    expect(forTarget).toHaveBeenCalledWith("contacts", "contacts_list");
    expect(extensions.textContent).toContain("CRM (crm)");
    expect(extensions.textContent).toContain("columns · append");
    expect(extensions.textContent).toContain("definition not found; not applied");
  });

  it("shows each recorded data call's raw response", async () => {
    viewDebugRecorder.sync(true, "/_m/contacts");
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ data: [{ id: "c1", name: "Ada" }] })));
    vi.stubGlobal("fetch", fetchMock);
    const { apiClient } = await import("@goerp/sdk");
    await apiClient.get("/contacts", { params: { limit: 25 } });
    vi.unstubAllGlobals();

    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const calls = section(overlay, "Data calls");

    await waitFor(() => expect(within(calls).getByText("GET /contacts?limit=25")).toBeTruthy());
    expect(calls.textContent).toContain('"name": "Ada"');
  });

  it("shows each evaluated condition and its result", async () => {
    viewDebugRecorder.sync(true, "/_m/contacts");
    viewDebugRecorder.recordCondition({
      scope: "contacts.contacts_list",
      location: 'column "iban" condition',
      expression: "user.roles contains 'finance'",
      kind: "visible",
      recordId: null,
      result: false,
      error: null,
    });
    viewDebugRecorder.recordCondition({
      scope: "contacts.contacts_list",
      location: 'action "archive" condition',
      expression: "record.status ==",
      kind: "visible",
      recordId: "c1",
      result: false,
      error: "unexpected end of expression",
    });

    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const conditions = section(overlay, "Conditions");

    await waitFor(() => expect(within(conditions).getByText("user.roles contains 'finance'")).toBeTruthy());
    expect(conditions.textContent).toContain("visible: false");
    expect(conditions.textContent).toContain("(record c1)");
    expect(conditions.textContent).toContain("error: unexpected end of expression; hidden");
  });

  it("shows the field security decision for each field of the view's model", async () => {
    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const fields = section(overlay, "Field security");
    const row = (name: string) => within(fields).getByText(name).closest("tr")?.textContent;

    expect(row("name")).toBe("nameshownno field access rule");
    expect(row("iban")).toBe(
      "ibanmaskedno read access, so the shell doesn't render it; responses return a masked value",
    );
    expect(row("salary")).toBe("salaryhiddenno read access, so the shell doesn't render it; responses omit the field");
  });

  it("shows why each navigation entry is hidden", async () => {
    const { overlay } = await renderOverlay("/_m/contacts?debug=views", CONTACT_LIST);
    const navigation = section(overlay, "Navigation");
    const line = (label: string) => within(navigation).getByText(label).closest("p")?.textContent;

    expect(line("All contacts")).toBe("All contacts shown");
    expect(line("VIP")).toBe('VIP hidden: missing permission "contacts:vip:read"');
    expect(line("People")).toBe('People hidden: module "hr" is not entitled');
    expect(line("Employees")).toBe('Employees hidden: group "People" is hidden');
  });

  it("says when the page renders no module view", async () => {
    const { overlay } = await renderOverlay("/settings?debug=views", null);

    expect(section(overlay, "Declaration").textContent).toContain("This page doesn't render a module view.");
    expect(section(overlay, "Field security").textContent).toContain("This page has no model resource.");
  });

  it("collapses its body and removes the debug parameter on close", async () => {
    const { overlay, router } = await renderOverlay("/_m/contacts?debug=views&sort=name", CONTACT_LIST);

    fireEvent.click(within(overlay).getByRole("button", { name: "Collapse" }));
    expect(within(overlay).getByRole("button", { name: "Expand" }).getAttribute("aria-expanded")).toBe("false");

    await act(async () => {
      fireEvent.click(within(overlay).getByRole("button", { name: "Close" }));
    });
    await waitFor(() => expect(router.state.location.search).toEqual({ sort: "name" }));
  });
});
