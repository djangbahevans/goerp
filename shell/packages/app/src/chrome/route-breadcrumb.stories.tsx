import { buildEmptyViewRegistry, type ResolvedView, ViewRegistryContext } from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, waitFor, within } from "storybook/test";
import { RouteBreadcrumb } from "./route-breadcrumb.js";

// A module view's trail comes from its resolved view (/_m/$ loader data), the module's display name, the
// resource's default list view and the record the form already fetched: the same cache entries are seeded here.
const RECORD_ID = "01a116c8-78a2-7476-bf8c-074624ac656c";
const RESOURCE = "contacts.contact";

function contactView(type: "list" | "form"): ResolvedView {
  return {
    module: "contacts",
    viewName: type === "list" ? "contacts_list" : "contacts_form",
    viewType: type,
    declaration: { name: "contacts_view", type, resource: RESOURCE, label: type === "list" ? "Contacts" : "Contact" },
    permissions: [],
    bundleUrl: null,
    ...(type === "form" ? { recordId: RECORD_ID } : {}),
  };
}

function withTrail(view: ResolvedView, path: string): Decorator {
  return () => {
    const client = new QueryClient();
    client.setQueryData(["breadcrumb", "resource-trail", "contacts", RESOURCE], {
      labelField: "display_name",
      list: { label: "Contacts", pathname: "/_m/contacts/contacts" },
    });
    client.setQueryData(["form-record", RESOURCE, RECORD_ID], {
      id: RECORD_ID,
      display_name: "Ama Mensah (Acme Ltd)",
    });
    const registry = { ...buildEmptyViewRegistry(), getModuleDisplayName: () => "Contacts" };
    const rootRoute = createRootRoute({
      component: () => (
        <ViewRegistryContext.Provider value={registry}>
          <QueryClientProvider client={client}>
            <div className="p-4">
              <RouteBreadcrumb />
            </div>
          </QueryClientProvider>
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
    return <RouterProvider router={router} />;
  };
}

const meta: Meta = { title: "Chrome/RouteBreadcrumb", parameters: { layout: "fullscreen" } };
export default meta;
type Story = StoryObj;

export const ListPage: Story = {
  name: "list page: one crumb, the module's label is not repeated",
  decorators: [withTrail(contactView("list"), "/_m/contacts/contacts")],
  play: async ({ canvasElement }) => {
    const nav = within(canvasElement).getByRole("navigation", { name: "Breadcrumb" });
    await waitFor(() => expect(within(nav).getAllByRole("listitem")).toHaveLength(1));
    await expect(within(nav).getByText("Contacts")).toHaveAttribute("aria-current", "page");
  },
};

const PHONE_VIEWPORT = {
  viewport: { options: { phone360: { name: "Phone (360px)", styles: { width: "360px", height: "740px" } } } },
};

const playRecordTrail: NonNullable<Story["play"]> = async ({ canvasElement }) => {
  const nav = within(canvasElement).getByRole("navigation", { name: "Breadcrumb" });
  await waitFor(() => expect(within(nav).getByText("Ama Mensah (Acme Ltd)")).toBeInTheDocument());
  await expect(within(nav).getByRole("link", { name: "Contacts" })).toHaveAttribute("href", "/_m/contacts/contacts");
  await expect(nav.textContent).not.toContain("01a116c8");
};

export const RecordPage: Story = {
  name: "record page: the list, then the record's name, with no id",
  decorators: [withTrail(contactView("form"), `/_m/contacts/contacts/${RECORD_ID}`)],
  play: playRecordTrail,
};

export const RecordPageOnPhone: Story = {
  name: "record page at 360px",
  parameters: PHONE_VIEWPORT,
  globals: { viewport: { value: "phone360", isRotated: false } },
  decorators: [withTrail(contactView("form"), `/_m/contacts/contacts/${RECORD_ID}`)],
  play: playRecordTrail,
};
