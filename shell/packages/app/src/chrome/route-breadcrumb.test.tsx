import { buildEmptyViewRegistry, type ResolvedView, ViewRegistryContext } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RouteBreadcrumb } from "./route-breadcrumb.js";

const { resolveResourceMock, resolveDeclarationMock, resolvePathMock } = vi.hoisted(() => ({
  resolveResourceMock: vi.fn(),
  resolveDeclarationMock: vi.fn(),
  resolvePathMock: vi.fn(),
}));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return {
    ...actual,
    resourceMetadataRegistry: { resolve: resolveResourceMock },
    viewDeclarationRegistry: { resolve: resolveDeclarationMock },
    viewPathRegistry: { resolve: resolvePathMock },
  };
});

afterEach(() => {
  cleanup();
  resolveResourceMock.mockReset();
  resolveDeclarationMock.mockReset();
  resolvePathMock.mockReset();
});

function Breadcrumb({ client }: { client: QueryClient }) {
  return (
    <QueryClientProvider client={client}>
      <RouteBreadcrumb />
    </QueryClientProvider>
  );
}

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

// Builds a single nested route chain (root -> segment[0] -> segment[1] ->
// ...), matching TanStack Router's real addChildren nesting convention —
// each segment is a relative path piece, not a full URL.
async function renderAt(path: string, segments: { segment: string; staticData?: { breadcrumb?: string } }[]) {
  const client = newClient();
  const rootRoute = createRootRoute({ component: () => <Breadcrumb client={client} /> });

  // biome-ignore lint/suspicious/noExplicitAny: TanStack Router's route generics don't unify across a dynamically-built chain; this is test-only plumbing.
  let parent: any = rootRoute;
  // biome-ignore lint/suspicious/noExplicitAny: see above.
  const chain: any[] = [];
  for (const def of segments) {
    const currentParent = parent;
    const route = createRoute({
      getParentRoute: () => currentParent,
      path: def.segment,
      staticData: def.staticData ?? {},
      component: () => null,
    });
    chain.push(route);
    parent = route;
  }

  // biome-ignore lint/suspicious/noExplicitAny: see above.
  let tree: any;
  for (let i = chain.length - 1; i >= 0; i--) {
    const route = chain[i];
    tree = tree ? route.addChildren([tree]) : route;
  }

  const routeTree = tree ? rootRoute.addChildren([tree]) : rootRoute.addChildren([]);
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("RouteBreadcrumb", () => {
  it("renders 'Home' for a real index route mounted at /", async () => {
    await renderAt("/", [{ segment: "/" }]);
    expect(screen.getByText("Home")).toBeTruthy();
  });

  it("falls back to a humanized last path segment when a route has no staticData.breadcrumb", async () => {
    await renderAt("/sales-orders", [{ segment: "sales-orders" }]);
    expect(screen.getByText("Sales Orders")).toBeTruthy();
  });

  it("uses staticData.breadcrumb when a route declares one", async () => {
    await renderAt("/orders", [{ segment: "orders", staticData: { breadcrumb: "Orders" } }]);
    expect(screen.getByText("Orders")).toBeTruthy();
  });

  it("marks the last crumb aria-current=page and renders it as non-interactive text", async () => {
    await renderAt("/sales/orders", [
      { segment: "sales", staticData: { breadcrumb: "Sales" } },
      { segment: "orders", staticData: { breadcrumb: "Orders" } },
    ]);
    const current = screen.getByText("Orders");
    expect(current.getAttribute("aria-current")).toBe("page");
    expect(current.tagName).toBe("SPAN");
  });

  it("renders non-last crumbs as real links", async () => {
    await renderAt("/sales/orders", [
      { segment: "sales", staticData: { breadcrumb: "Sales" } },
      { segment: "orders", staticData: { breadcrumb: "Orders" } },
    ]);
    const link = screen.getByText("Sales");
    expect(link.tagName).toBe("A");
  });

  it("collapses trails beyond 3 levels into first, ellipsis, last two", async () => {
    await renderAt("/a/b/c/d", [
      { segment: "a", staticData: { breadcrumb: "A" } },
      { segment: "b", staticData: { breadcrumb: "B" } },
      { segment: "c", staticData: { breadcrumb: "C" } },
      { segment: "d", staticData: { breadcrumb: "D" } },
    ]);
    // Exactly 4 crumbs: first (A) + last two (C, D) stay visible; only the
    // single one in between (B) collapses into the ellipsis.
    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.queryByText("B")).toBeNull();
    expect(screen.getByText("C")).toBeTruthy();
    expect(screen.getByText("D")).toBeTruthy();
    const ellipsis = screen.getByText("…");
    expect(ellipsis.getAttribute("aria-label")).toBe("Hidden breadcrumb levels: B");
  });

  it("does not collapse a trail of exactly 3 levels", async () => {
    await renderAt("/a/b/c", [
      { segment: "a", staticData: { breadcrumb: "A" } },
      { segment: "b", staticData: { breadcrumb: "B" } },
      { segment: "c", staticData: { breadcrumb: "C" } },
    ]);
    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("C")).toBeTruthy();
    expect(screen.queryByText("…")).toBeNull();
  });

  it("skips a pathless layout route's match instead of rendering a duplicate crumb", async () => {
    const client = newClient();
    const rootRoute = createRootRoute({ component: () => <Breadcrumb client={client} /> });
    // A pathless route (id, no path) contributes a match sharing its
    // child's pathname — e.g. an auth-gate or shared layout wrapper.
    const layoutRoute = createRoute({ getParentRoute: () => rootRoute, id: "_layout", component: () => null });
    const ordersRoute = createRoute({
      getParentRoute: () => layoutRoute,
      path: "orders",
      staticData: { breadcrumb: "Orders" },
      component: () => null,
    });
    const routeTree = rootRoute.addChildren([layoutRoute.addChildren([ordersRoute])]);
    const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ["/orders"] }) });
    await router.load();
    render(<RouterProvider router={router} />);

    expect(screen.getAllByText("Orders")).toHaveLength(1);
  });
});

const CONTACT_ID = "01a116c8-78a2-7476-bf8c-074624ac656c";

function contactView(
  overrides: Partial<ResolvedView> & { declarationType?: string; label?: string } = {},
): ResolvedView {
  const { declarationType = "form", label = "Contact", ...rest } = overrides;
  return {
    module: "contacts",
    viewName: declarationType === "form" ? "contacts_form" : "contacts_list",
    viewType: declarationType,
    declaration: { name: "contacts_view", type: declarationType, resource: "contacts.contact", label },
    permissions: [],
    bundleUrl: null,
    ...rest,
  };
}

async function renderModuleView(
  path: string,
  view: ResolvedView,
  options: { client?: QueryClient; moduleName?: string | null } = {},
) {
  const client = options.client ?? newClient();
  const registry = { ...buildEmptyViewRegistry(), getModuleDisplayName: () => options.moduleName ?? "Contacts" };
  const rootRoute = createRootRoute({
    component: () => (
      <ViewRegistryContext.Provider value={registry}>
        <Breadcrumb client={client} />
      </ViewRegistryContext.Provider>
    ),
  });
  const moduleRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/_m/$",
    loader: () => view,
    component: () => null,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([moduleRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return client;
}

function crumbLabels(): string[] {
  return screen.getAllByRole("listitem").map((item) => item.textContent?.replace("›", "").trim() ?? "");
}

function mockContactSchema() {
  resolveResourceMock.mockResolvedValue({ labelField: "display_name", defaultListView: "contacts_list" });
  resolveDeclarationMock.mockResolvedValue({
    name: "contacts_list",
    type: "list",
    resource: "contacts.contact",
    label: "Contacts",
  });
  resolvePathMock.mockResolvedValue("/contacts/contacts");
}

describe("RouteBreadcrumb for module views", () => {
  it("shows a single crumb for a list page whose label repeats the module's", async () => {
    await renderModuleView("/_m/contacts/contacts", contactView({ declarationType: "list", label: "Contacts" }));

    expect(crumbLabels()).toEqual(["Contacts"]);
    expect(screen.getByText("Contacts").getAttribute("aria-current")).toBe("page");
  });

  it("shows module and list labels for a list page that differ", async () => {
    await renderModuleView(
      "/_m/sales/orders",
      contactView({ module: "sales", declarationType: "list", label: "Orders" }),
      { moduleName: "Sales" },
    );

    expect(crumbLabels()).toEqual(["Sales", "Orders"]);
  });

  it("shows the record's label under its list, with no id anywhere in the trail", async () => {
    mockContactSchema();
    const client = newClient();
    client.setQueryData(["form-record", "contacts.contact", CONTACT_ID], {
      id: CONTACT_ID,
      display_name: "Ama Mensah (Acme Ltd)",
    });
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }), { client });

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "Ama Mensah (Acme Ltd)"]));
    expect(document.body.textContent).not.toContain(CONTACT_ID.slice(0, 8));
    expect(screen.getByRole("link", { name: "Contacts" }).getAttribute("href")).toBe("/_m/contacts/contacts");
    expect(screen.getByText("Ama Mensah (Acme Ltd)").getAttribute("aria-current")).toBe("page");
  });

  it("shows the view's label while the record loads", async () => {
    mockContactSchema();
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }));

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "Contact"]));
  });

  it("keeps the view's label when the record cannot be read", async () => {
    mockContactSchema();
    const client = newClient();
    client.setQueryDefaults(["form-record"], { queryFn: () => Promise.reject(new Error("forbidden")) });
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }), { client });

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "Contact"]));
  });

  it("keeps the view's label when the record has no value in the label field", async () => {
    mockContactSchema();
    const client = newClient();
    client.setQueryData(["form-record", "contacts.contact", CONTACT_ID], { id: CONTACT_ID, display_name: "" });
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }), { client });

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "Contact"]));
  });

  it("keeps the list link when the record's label repeats it", async () => {
    mockContactSchema();
    const client = newClient();
    client.setQueryData(["form-record", "contacts.contact", CONTACT_ID], { id: CONTACT_ID, display_name: "Contacts" });
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }), { client });

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "Contacts"]));
    expect(screen.getAllByText("Contacts")[0]?.tagName).toBe("A");
  });

  it("finds the list view in the module that owns the resource", async () => {
    resolveResourceMock.mockResolvedValue({ module: "crm", labelField: "name", defaultListView: "people_list" });
    resolveDeclarationMock.mockImplementation(async (_view: string, module: string) =>
      module === "crm" ? { name: "people_list", type: "list", resource: "contacts.contact", label: "People" } : null,
    );
    resolvePathMock.mockImplementation(async (_view: string, module: string) =>
      module === "crm" ? "/crm/people" : null,
    );
    await renderModuleView(`/_m/contacts/contacts/${CONTACT_ID}`, contactView({ recordId: CONTACT_ID }));

    await waitFor(() => expect(crumbLabels()).toEqual(["Contacts", "People", "Contact"]));
  });

  it("shows no crumb for a module route before its view has resolved", async () => {
    const rootRoute = createRootRoute({ component: () => <Breadcrumb client={newClient()} /> });
    const moduleRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/_m/$",
      loader: () => null,
      component: () => null,
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([moduleRoute]),
      history: createMemoryHistory({ initialEntries: [`/_m/contacts/contacts/${CONTACT_ID}`] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);

    expect(screen.queryAllByRole("listitem")).toHaveLength(0);
  });

  it("shows the module name as plain text when the list view differs from it", async () => {
    resolveResourceMock.mockResolvedValue({ labelField: "name", defaultListView: "orders_list" });
    resolveDeclarationMock.mockResolvedValue({
      name: "orders_list",
      type: "list",
      resource: "sales.order",
      label: "Orders",
    });
    resolvePathMock.mockResolvedValue("/sales/orders");
    const client = newClient();
    client.setQueryData(["form-record", "sales.order", "o1"], { id: "o1", name: "ORD-0042" });
    await renderModuleView(
      "/_m/sales/orders/o1",
      contactView({
        module: "sales",
        recordId: "o1",
        declaration: { name: "order_form", type: "form", resource: "sales.order", label: "Order" },
      }),
      { client, moduleName: "Sales" },
    );

    await waitFor(() => expect(crumbLabels()).toEqual(["Sales", "Orders", "ORD-0042"]));
    expect(screen.getByText("Sales").tagName).toBe("SPAN");
    expect(screen.getByRole("link", { name: "Orders" })).toBeTruthy();
  });

  it("shows the view's label for a create form, which names no record", async () => {
    await renderModuleView("/_m/contacts/contacts/new", contactView());

    expect(crumbLabels()).toEqual(["Contacts", "Contact"]);
    expect(resolveResourceMock).not.toHaveBeenCalled();
  });
});
