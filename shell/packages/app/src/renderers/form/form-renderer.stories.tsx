import type { APIClient } from "@goerp/sdk";
import type { ResourceRegistry } from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import type { Row } from "../list/list-view-types.js";
import { FormRenderer } from "./form-renderer.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import { recordQueryKey } from "./use-form-record.js";

// shell-architecture.md §20's FormRenderer does real data-fetching
// internally (useFormRecord) and ListActions reads route state via
// TanStack Router unconditionally — every story seeds the exact
// react-query cache entry useFormRecord's own useQuery reads, the same way
// list-renderer.stories.tsx does, rather than mocking @goerp/sdk.
//
// The manual-save "saving"/"error" and autosave "saving" stories below are
// the one exception to that: a save mutation has no query-cache key to
// seed the way a query does, so they instead go through
// testFormRecordOptions — FormRendererProps' own test-only seam onto
// useFormRecord's registry/client/autoSaveDelay injection.

// A resolvable registry entry is enough for saveRecord to compute a path —
// which path/method doesn't matter to any story here, since the fake
// `client` below is what actually observes the call.
function fakeRegistry(): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: async () => ({
      module: MODULE,
      resource: view.resource,
      listPath: "/contacts",
      getPath: "/contacts/{id}",
      createPath: "/contacts",
      updatePath: "/contacts/{id}",
      deletePath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
    }),
  };
}

// `get` is never actually called by these stories — the record comes from
// defaultClient()'s already-seeded query cache — but useFormRecord's
// `client` option replaces the whole client, query included, so the full
// shape is still required.
function fakeClient(overrides: Record<string, unknown>): Pick<APIClient, "get" | "post" | "put" | "patch"> {
  return {
    get: async () => ({}),
    post: async () => ({}),
    put: async () => ({}),
    patch: async () => ({}),
    ...overrides,
  } as unknown as Pick<APIClient, "get" | "post" | "put" | "patch">;
}

const MODULE = "contacts";
const RECORD_ID = "c1";

const view: FormViewDeclaration = {
  name: "contact_form",
  type: "form",
  resource: "contacts.contact",
  label: "Contact",
  sections: [
    {
      type: "header",
      fields: [
        { field: "name", label: "Name", type: "text" },
        { field: "status", label: "Status", type: "text", readonly: true },
      ],
    },
    {
      type: "fields",
      label: "Details",
      collapsible: true,
      fields: [
        { field: "email", label: "Email", type: "email" },
        { field: "phone", label: "Phone", type: "phone" },
        { field: "website", label: "Website", type: "url" },
        { field: "employee_count", label: "Employees", type: "number" },
      ],
    },
    {
      type: "fields",
      label: "Preferences",
      columns: 3,
      fields: [
        {
          field: "plan",
          label: "Plan",
          type: "select",
          options: [
            { value: "free", label: "Free" },
            { value: "pro", label: "Pro" },
            { value: "enterprise", label: "Enterprise" },
          ],
        },
        { field: "is_vip", label: "VIP", type: "boolean" },
        { field: "founded", label: "Founded", type: "date" },
        // relation.md's own single-value case — safely FieldWrapper-wrapped
        // (form-fields.test.tsx's "relation (single, already holding a
        // value)" test is what proves that, not this story).
        { field: "account_manager_id", label: "Account Manager", type: "relation", resource: "contacts.user", span: 2 },
        // Inherently multi-valued — kept on the explicit-label path
        // (usesImplicitLabelWrap in form-fields.tsx), still fully styled by
        // TagsField's own design, just without FieldWrapper's outer chrome.
        { field: "tag_ids", label: "Tags", type: "tags", resource: "contacts.tag" },
      ],
    },
    {
      type: "sub_list",
      label: "Addresses",
      inline_key: "addresses",
      columns: [
        { field: "city", label: "City" },
        { field: "country", label: "Country" },
      ],
    },
  ],
  tabs: [
    { label: "Notes", type: "fields", sections: [{ type: "fields", fields: [{ field: "notes", label: "Notes" }] }] },
    {
      label: "Billing",
      type: "fields",
      sections: [{ type: "fields", fields: [{ field: "billing_email", label: "Billing Email", type: "email" }] }],
    },
  ],
  sidebar: { width: 240, sections: [{ label: "Overview", fields: ["status", "phone"] }] },
  chatter: false,
};

const RECORD: Row = {
  id: RECORD_ID,
  name: "Acme Corp",
  status: "Active",
  email: "hello@acme.example",
  phone: "+15551234567",
  website: "https://acme.example",
  employee_count: 240,
  plan: "pro",
  is_vip: true,
  founded: "2016-04-01",
  account_manager: { id: "u1", display_name: "Jordan Lee" },
  tags: [{ id: "t1", name: "wholesale" }],
  addresses: [
    { city: "Accra", country: "Ghana" },
    { city: "Kumasi", country: "Ghana" },
  ],
  notes: "Called customer to confirm renewal.",
  billing_email: "billing@acme.example",
};

function seededClient(): QueryClient {
  return new QueryClient({
    // retryOnMount defaults true independent of `retry` — without it, a
    // component mounting onto a query this file already settled to an
    // error (errorClient, below) triggers a real refetch against
    // Storybook's own (absent) backend, silently overwriting the seeded
    // message with whatever that fetch's own error happens to be.
    defaultOptions: { queries: { retry: false, retryOnMount: false, staleTime: Number.POSITIVE_INFINITY } },
  });
}

function defaultClient(): QueryClient {
  const client = seededClient();
  client.setQueryData(recordQueryKey(view.resource, RECORD_ID), RECORD);
  return client;
}

// A prefetch whose queryFn never resolves settles useQuery into a
// permanent loading state — same in-flight-query-dedup trick
// list-renderer.stories.tsx's own loadingClient uses.
function loadingClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: recordQueryKey(view.resource, RECORD_ID),
    queryFn: () => new Promise<never>(() => {}),
  });
  return client;
}

function errorClient(): QueryClient {
  const client = seededClient();
  void client.prefetchQuery({
    queryKey: recordQueryKey(view.resource, RECORD_ID),
    queryFn: () => Promise.reject(new Error("Couldn't reach the server.")),
  });
  return client;
}

// Wraps in both QueryClientProvider (the cache useFormRecord reads) and
// RouterProvider (ListActions' useNavigate runs unconditionally, even with
// an empty header_actions array).
function withFormProviders(client: QueryClient): Decorator {
  return (Story) => {
    const rootRoute = createRootRoute();
    const indexRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: () => (
        <QueryClientProvider client={client}>
          <Story />
        </QueryClientProvider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([indexRoute]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const meta: Meta<typeof FormRenderer> = {
  title: "Renderers/FormRenderer",
  component: FormRenderer,
  args: { view, module: MODULE, recordId: RECORD_ID },
};

export default meta;

type Story = StoryObj<typeof FormRenderer>;

// Storybook composes decorators across meta/story levels rather than
// overriding — each story below sets its own provider stack explicitly, a
// distinct QueryClient per story, instead of a shared meta-level one.
export const Loading: Story = {
  decorators: [withFormProviders(loadingClient())],
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('[data-skeleton="lines"]')).toBeInTheDocument();
  },
};

export const ErrorState: Story = {
  name: "load error, with a working Retry action",
  decorators: [withFormProviders(errorClient())],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("Couldn't reach the server.")).toBeInTheDocument();
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};

export const Default: Story = {
  name: "loaded: header section, two fields sections (one collapsible), inline sub_list",
  decorators: [withFormProviders(defaultClient())],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);

    // "header"-type section: fields render directly, no SectionCard title.
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());

    // "fields"-type section: SectionCard title + collapse toggle.
    expect(canvas.getByRole("heading", { name: "Details" })).toBeInTheDocument();
    expect(canvas.getByLabelText("Email")).toBeInTheDocument();

    // "Preferences": a 3-column section mixing select/boolean/date/relation/
    // tags — relation (single) goes through FieldWrapper same as any other
    // safe type; tags keeps its own explicit-label styling (see
    // usesImplicitLabelWrap in form-fields.tsx).
    expect(canvas.getByRole("heading", { name: "Preferences" })).toBeInTheDocument();
    expect(canvas.getByLabelText("VIP")).toBeChecked();
    expect(canvas.getByDisplayValue("Jordan Lee")).toBeInTheDocument();
    expect(canvas.getByText("wholesale")).toBeInTheDocument();

    // "sub_list"-type section, inline_key: SectionCard-wrapped inline table.
    expect(canvas.getByRole("heading", { name: "Addresses" })).toBeInTheDocument();
    expect(canvas.getByText("Accra")).toBeInTheDocument();
    expect(canvas.getByText("Kumasi")).toBeInTheDocument();

    // Save starts disabled — nothing is dirty yet.
    expect(canvas.getByRole("button", { name: "Save" })).toBeDisabled();

    // Collapsing "Details" removes its content from the accessibility tree
    // without unmounting it (list-renderer.md/form-renderer.md's own
    // mounted-but-hidden requirement, verified here via the query going
    // from found to not-found rather than checking for a class).
    // getByRole (not getByLabelText, which ignores [hidden]) is what
    // actually exercises the accessibility-tree removal SectionCard's
    // `hidden` attribute is for.
    await userEvent.click(canvas.getByRole("button", { name: "Collapse" }));
    expect(canvas.queryByRole("textbox", { name: "Email" })).not.toBeInTheDocument();
    await userEvent.click(canvas.getByRole("button", { name: "Expand" }));
    expect(canvas.getByRole("textbox", { name: "Email" })).toBeInTheDocument();

    // FormSidebarRenderer -> Sidebar/SectionCard/Field (goerp#774).
    expect(canvas.getByRole("heading", { name: "Overview" })).toBeInTheDocument();
    expect(canvas.getByText("Active")).toBeInTheDocument();

    // FormTabsRenderer -> Tabs/TabPanel (goerp#774): starts on the first
    // tab, switches on click, and the other tab's content isn't mounted.
    expect(canvas.getByDisplayValue("Called customer to confirm renewal.")).toBeInTheDocument();
    expect(canvas.queryByLabelText("Billing Email")).not.toBeInTheDocument();
    await userEvent.click(canvas.getByRole("tab", { name: "Billing" }));
    expect(canvas.getByLabelText("Billing Email")).toBeInTheDocument();
    expect(canvas.queryByDisplayValue("Called customer to confirm renewal.")).not.toBeInTheDocument();
  },
};

export const ManualSaveDirty: Story = {
  name: "manual save: dirty enables Save",
  decorators: [withFormProviders(defaultClient())],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());

    const saveButton = canvas.getByRole("button", { name: "Save" });
    expect(saveButton).toBeDisabled();

    await userEvent.type(canvas.getByLabelText("Phone"), "9");
    await waitFor(() => expect(saveButton).toBeEnabled());
  },
};

export const ManualSaveSaving: Story = {
  name: "manual save: Save shows a loading state while the mutation is in flight",
  decorators: [withFormProviders(defaultClient())],
  args: {
    testFormRecordOptions: {
      registry: fakeRegistry(),
      client: fakeClient({ put: () => new Promise<never>(() => {}) }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());
    await userEvent.type(canvas.getByLabelText("Phone"), "9");

    const saveButton = canvas.getByRole("button", { name: "Save" });
    await userEvent.click(saveButton);
    await waitFor(() => expect(saveButton).toHaveAttribute("aria-busy", "true"));
  },
};

export const ManualSaveError: Story = {
  name: "manual save: a rejected mutation surfaces saveError",
  decorators: [withFormProviders(defaultClient())],
  args: {
    testFormRecordOptions: {
      registry: fakeRegistry(),
      client: fakeClient({
        put: async () => {
          throw new Error("Couldn't save — conflict.");
        },
      }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());
    await userEvent.type(canvas.getByLabelText("Phone"), "9");
    await userEvent.click(canvas.getByRole("button", { name: "Save" }));

    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("Couldn't save — conflict.")).toBeInTheDocument();
  },
};

export const Autosave: Story = {
  name: "autosave: no Save button rendered at all",
  args: { view: { ...view, autosave: true } },
  decorators: [withFormProviders(defaultClient())],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());
    expect(canvas.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  },
};

export const AutosaveSaving: Story = {
  name: "autosave: shows a saving indicator while a save is in flight",
  decorators: [withFormProviders(defaultClient())],
  args: {
    view: { ...view, autosave: true },
    testFormRecordOptions: {
      registry: fakeRegistry(),
      client: fakeClient({ put: () => new Promise<never>(() => {}) }),
      // Real default (2000ms) would make this play function slow and,
      // worse, flaky under CI load — the debounce itself isn't what's
      // under test here, just the "Saving…" indicator it eventually fires.
      autoSaveDelay: 10,
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByDisplayValue("Acme Corp")).toBeInTheDocument());
    await userEvent.type(canvas.getByLabelText("Phone"), "9");
    await waitFor(() => expect(canvas.getByRole("status")).toHaveTextContent("Saving…"));
  },
};
